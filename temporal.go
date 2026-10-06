package surriti

import (
	"context"
	"strings"
	"time"
)

func FindSimilarEdges(
	ctx context.Context,
	driver Queryer,
	fact string,
	factEmbedding []float64,
	groupID string,
	limit int,
	coSubjectUUID *string,
	coObjectUUID *string,
	onlyActive bool,
) ([]EntityEdge, error) {
	if limit == 0 {
		limit = 10
	}
	rows := []map[string]any{}
	g := groupID
	if factEmbedding != nil {
		hits, err := VectorSearchEdges(ctx, driver, factEmbedding, &g, limit, false, nil)
		if err != nil {
			return nil, err
		}
		rows = append(rows, hits...)
	}
	if fact != "" {
		hits, err := FulltextSearchEdges(ctx, driver, fact, &g, limit, false, nil)
		if err != nil {
			return nil, err
		}
		rows = append(rows, hits...)
	}

	if len(rows) == 0 {
		conditions := []string{`group_id = $group_id`, `status = "active"`, "invalid_at IS NONE"}
		params := map[string]any{"group_id": groupID, "limit": limit * 2}
		if coSubjectUUID != nil {
			conditions = append(conditions, `in = type::record("entity", $subj)`)
			params["subj"] = *coSubjectUUID
		}
		if coObjectUUID != nil {
			conditions = append(conditions, `out = type::record("entity", $obj)`)
			params["obj"] = *coObjectUUID
		}
		result, err := driver.Query(ctx,
			"SELECT * FROM relates_to WHERE "+strings.Join(conditions, " AND ")+" LIMIT $limit;",
			params,
		)
		if err != nil {
			return nil, err
		}
		rows = UnwrapRows(result)
	}

	if coObjectUUID != nil {
		result, err := driver.Query(ctx, `
SELECT * FROM relates_to
WHERE group_id = $group_id
    AND out = type::record("entity", $obj)
    AND status = "active"
    AND invalid_at IS NONE
LIMIT $limit;`, map[string]any{"group_id": groupID, "obj": *coObjectUUID, "limit": limit * 2})
		if err != nil {
			return nil, err
		}
		rows = append(rows, UnwrapRows(result)...)
	} else if coSubjectUUID != nil {
		result, err := driver.Query(ctx, `
SELECT * FROM relates_to
WHERE group_id = $group_id
    AND in = type::record("entity", $sub)
    AND status = "active"
    AND invalid_at IS NONE
LIMIT $limit;`, map[string]any{"group_id": groupID, "sub": *coSubjectUUID, "limit": limit * 2})
		if err != nil {
			return nil, err
		}
		rows = append(rows, UnwrapRows(result)...)
	}

	seen := map[string]struct{}{}
	edges := make([]EntityEdge, 0, len(rows))
	for _, row := range rows {
		uid := stringFromAny(row["uuid"])
		if uid == "" {
			uid = stripRecordID(row["id"])
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		e := ParseEdge(row)
		if onlyActive && (e.Status != "active" || e.InvalidAt != nil) {
			continue
		}
		edges = append(edges, e)
	}
	return edges, nil
}

func InvalidateEdges(ctx context.Context, driver Queryer, edgeUUIDs []string, invalidAt time.Time, supersededBy *string) error {
	if len(edgeUUIDs) == 0 {
		return nil
	}
	_, err := driver.Query(ctx, `
UPDATE relates_to
SET invalid_at = $invalid_at, expired_at = $expired_at,
    status = "superseded", superseded_by = $superseded_by
WHERE uuid IN $uuids AND (invalid_at IS NONE OR invalid_at > $invalid_at);
`, map[string]any{
		"uuids":         edgeUUIDs,
		"invalid_at":    invalidAt,
		"expired_at":    utcNow(),
		"superseded_by": supersededBy,
	})
	return err
}

func ResolveContradictions(
	ctx context.Context,
	driver Queryer,
	llm LLMClient,
	newFact string,
	newFactEmbedding []float64,
	newValidAt time.Time,
	groupID string,
	similarityLimit int,
	newFactStruct *ExtractedFact,
	newEdgeUUID *string,
	newSubjectUUID *string,
	newObjectUUID *string,
) ([]EntityEdge, error) {
	if similarityLimit == 0 {
		similarityLimit = 10
	}
	edges, err := FindSimilarEdges(ctx, driver, newFact, newFactEmbedding, groupID, similarityLimit, newSubjectUUID, newObjectUUID, true)
	if err != nil {
		return nil, err
	}
	filtered := make([]EntityEdge, 0, len(edges))
	for _, e := range edges {
		if newEdgeUUID != nil && e.UUID == *newEdgeUUID {
			continue
		}
		// Preserve Python's OR-based compatibility check for legacy rows
		// with inconsistent is_belief and memory_class fields.
		if e.IsBelief && e.MemoryClass == "belief" {
			continue
		}
		filtered = append(filtered, e)
	}
	edges = filtered
	if len(edges) == 0 {
		return []EntityEdge{}, nil
	}
	facts := make([]string, len(edges))
	structured := []ContradictionCandidate(nil)
	if newFactStruct != nil {
		structured = make([]ContradictionCandidate, 0, len(edges))
	}
	for i, e := range edges {
		facts[i] = e.Fact
		if structured != nil {
			c := ContradictionCandidate{
				UUID: e.UUID, Subject: e.SourceNodeUUID, Predicate: e.Name,
				Object: e.TargetNodeUUID, Fact: e.Fact, Domain: e.Domain,
			}
			if e.ValidAt != nil {
				v := e.ValidAt.Format(time.RFC3339Nano)
				c.ValidAt = &v
			}
			if e.InvalidAt != nil {
				v := e.InvalidAt.Format(time.RFC3339Nano)
				c.InvalidAt = &v
			}
			structured = append(structured, c)
		}
	}
	indices, err := llm.FindContradictions(ctx, ContradictionRequest{
		NewFact: newFact, ExistingFacts: facts, Candidates: structured, NewFactStruct: newFactStruct,
	})
	if err != nil {
		return nil, err
	}
	if len(indices) == 0 {
		return []EntityEdge{}, nil
	}
	invalidated := make([]EntityEdge, 0, len(indices))
	uuids := make([]string, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 || idx >= len(edges) {
			continue
		}
		invalidated = append(invalidated, edges[idx])
		uuids = append(uuids, edges[idx].UUID)
	}
	if err := InvalidateEdges(ctx, driver, uuids, newValidAt, newEdgeUUID); err != nil {
		return nil, err
	}
	return invalidated, nil
}
