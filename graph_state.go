package surriti

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Surriti) RegisterFrame(frame RelationFrame, groupID *string) RelationFrame {
	g := ""
	if groupID != nil {
		g = *groupID
	}
	return s.RelationFrames.Register(frame, g)
}

func (s *Surriti) GetFrame(predicate, groupID string) (*RelationFrame, bool) {
	f, ok := s.RelationFrames.Get(predicate, groupID)
	if !ok {
		return nil, false
	}
	return &f, true
}

func (s *Surriti) MergeFrames(source, target string, groupID *string, strategy string) (RelationFrame, error) {
	if strategy == "" {
		strategy = "alias"
	}
	if strategy != "alias" {
		return RelationFrame{}, fmt.Errorf("unsupported merge strategy %q; only 'alias' is implemented", strategy)
	}
	g := ""
	if groupID != nil {
		g = *groupID
	}
	src, ok := s.RelationFrames.Get(source, g)
	if !ok {
		return RelationFrame{}, fmt.Errorf("%w: merge_frames unknown source frame %q", ErrNotFound, source)
	}
	tgt, ok := s.RelationFrames.Get(target, g)
	if !ok {
		return RelationFrame{}, fmt.Errorf("%w: merge_frames unknown target frame %q", ErrNotFound, target)
	}
	if src.CanonicalName == tgt.CanonicalName {
		return tgt, nil
	}
	existing := map[string]struct{}{}
	for _, a := range tgt.Aliases {
		existing[strings.ToLower(a)] = struct{}{}
	}
	aliases := append([]string(nil), tgt.Aliases...)
	for _, cand := range append([]string{src.CanonicalName}, src.Aliases...) {
		key := lowerTrim(cand)
		if key == "" || key == lowerTrim(tgt.CanonicalName) {
			continue
		}
		if _, seen := existing[key]; seen {
			continue
		}
		existing[key] = struct{}{}
		aliases = append(aliases, key)
	}
	tgt.Aliases = aliases
	s.RelationFrames.Register(tgt, g)
	return tgt, nil
}

func (s *Surriti) GetConflicts(ctx context.Context, groupID string, limit int) ([]EntityEdge, error) {
	if limit == 0 {
		limit = 100
	}
	result, err := s.Driver.Query(ctx,
		`SELECT * FROM relates_to WHERE group_id = $group_id AND status = "needs_resolution" LIMIT $limit;`,
		map[string]any{"group_id": groupID, "limit": limit},
	)
	if err != nil {
		return nil, err
	}
	rows := UnwrapRows(result)
	out := make([]EntityEdge, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParseEdge(row))
	}
	return out, nil
}

func (s *Surriti) GetCurrentFacts(ctx context.Context, subjectUUID, groupID string, predicate, domain *string, limit int) ([]EntityEdge, error) {
	if limit == 0 {
		limit = 50
	}
	clauses := []string{
		"group_id = $group_id",
		`in = type::record("entity", $src)`,
		`status = "active"`,
		"invalid_at IS NONE",
	}
	params := map[string]any{"group_id": groupID, "src": subjectUUID, "limit": limit}
	if predicate != nil {
		clauses = append(clauses, "name = $name")
		params["name"] = *predicate
	}
	if domain != nil {
		clauses = append(clauses, "domain = $domain")
		params["domain"] = *domain
	}
	q := "SELECT * FROM relates_to WHERE " + strings.Join(clauses, " AND ") + " ORDER BY valid_at DESC LIMIT $limit;"
	result, err := s.Driver.Query(ctx, q, params)
	if err != nil {
		return nil, err
	}
	rows := UnwrapRows(result)
	out := make([]EntityEdge, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParseEdge(row))
	}
	return out, nil
}

func (s *Surriti) GetCurrentFact(ctx context.Context, subjectUUID, predicate, groupID string) (*EntityEdge, error) {
	edges, err := s.GetCurrentFacts(ctx, subjectUUID, groupID, &predicate, nil, 1)
	if err != nil || len(edges) == 0 {
		return nil, err
	}
	return &edges[0], nil
}

func (s *Surriti) CurrentProfile(ctx context.Context, subjectUUID, groupID string, limit int) (map[string][]EntityEdge, error) {
	if limit == 0 {
		limit = 200
	}
	edges, err := s.GetCurrentFacts(ctx, subjectUUID, groupID, nil, nil, limit)
	if err != nil {
		return nil, err
	}
	out := map[string][]EntityEdge{}
	for _, edge := range edges {
		key := edge.CanonicalName
		if key == "" {
			key = edge.Name
		}
		out[key] = append(out[key], edge)
	}
	return out, nil
}

func (s *Surriti) GetFactsAsOf(ctx context.Context, subjectUUID string, asOf time.Time, groupID string, predicate, domain *string, limit int) ([]EntityEdge, error) {
	if limit == 0 {
		limit = 200
	}
	clauses := []string{
		"group_id = $group_id",
		`in = type::record("entity", $src)`,
		"(valid_at IS NONE OR valid_at <= $as_of)",
		"(invalid_at IS NONE OR invalid_at > $as_of)",
		"(expired_at IS NONE OR expired_at > $as_of)",
	}
	params := map[string]any{"group_id": groupID, "src": subjectUUID, "as_of": asOf, "limit": limit}
	if predicate != nil {
		clauses = append(clauses, "name = $name")
		params["name"] = *predicate
	}
	if domain != nil {
		clauses = append(clauses, "domain = $domain")
		params["domain"] = *domain
	}
	q := "SELECT * FROM relates_to WHERE " + strings.Join(clauses, " AND ") + " ORDER BY valid_at DESC LIMIT $limit;"
	result, err := s.Driver.Query(ctx, q, params)
	if err != nil {
		return nil, err
	}
	rows := UnwrapRows(result)
	out := make([]EntityEdge, 0, len(rows))
	for _, row := range rows {
		out = append(out, ParseEdge(row))
	}
	return out, nil
}

func (s *Surriti) GetStateAsOf(ctx context.Context, subjectUUID string, asOf time.Time, groupID string, predicate, domain *string) (map[string]EntityEdge, error) {
	edges, err := s.GetFactsAsOf(ctx, subjectUUID, asOf, groupID, predicate, domain, 200)
	if err != nil {
		return nil, err
	}
	out := map[string]EntityEdge{}
	for _, edge := range edges {
		key := edge.Name + "\x1f" + edge.TargetNodeUUID
		existing, ok := out[key]
		if !ok {
			out[key] = edge
			continue
		}
		ev := time.Time{}
		if edge.ValidAt != nil {
			ev = *edge.ValidAt
		}
		xv := time.Time{}
		if existing.ValidAt != nil {
			xv = *existing.ValidAt
		}
		if ev.After(xv) {
			out[key] = edge
		}
	}
	return out, nil
}
