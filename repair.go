package surriti

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

type ReconciliationPolicy string

const (
	ReconcileMergeIdentical ReconciliationPolicy = "merge-identical"
	ReconcileMergeByFactKey ReconciliationPolicy = "merge-by-fact-key"
)

type FactKeyCollision struct {
	GroupID string
	FactKey string
	Edges   []map[string]any
}

var mergeableCollisionFields = map[string]struct{}{
	"id": {}, "uuid": {}, "episodes": {}, "created_at": {}, "updated_at": {}, "fact_embedding": {},
	"reinforcement_count": {}, "last_reinforced_at": {}, "recall_count": {}, "last_recalled_at": {},
	"weight": {}, "decay_score": {}, "attributes": {}, "fact_key": {},
}

func semanticPayload(edge map[string]any) map[string]any {
	out := make(map[string]any, len(edge))
	for k, v := range edge {
		if _, mergeable := mergeableCollisionFields[k]; mergeable {
			continue
		}
		out[k] = v
	}
	return out
}

func canonicalCollisionEdgeIndex(edges []map[string]any) int {
	indices := make([]int, len(edges))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		a := edges[indices[i]]
		b := edges[indices[j]]
		aa := collisionSortKey(a)
		bb := collisionSortKey(b)
		if aa.notActive != bb.notActive {
			return !aa.notActive
		}
		if aa.closed != bb.closed {
			return !aa.closed
		}
		if !aa.created.Equal(bb.created) {
			if aa.created.IsZero() {
				return true
			}
			if bb.created.IsZero() {
				return false
			}
			return aa.created.Before(bb.created)
		}
		return aa.uuid < bb.uuid
	})
	if len(indices) == 0 {
		return -1
	}
	return indices[0]
}

type collisionKey struct {
	notActive bool
	closed    bool
	created   time.Time
	uuid      string
}

func collisionSortKey(edge map[string]any) collisionKey {
	status := stringFromAny(edge["status"])
	if status == "" {
		status = "active"
	}
	var created time.Time
	if t := asTimePtr(edge["created_at"]); t != nil {
		created = t.UTC()
	}
	return collisionKey{
		notActive: status != "active",
		closed:    edge["invalid_at"] != nil || edge["expired_at"] != nil,
		created:   created,
		uuid:      stringFromAny(edge["uuid"]),
	}
}

func InspectFactKeyCollisions(ctx context.Context, driver Queryer) ([]FactKeyCollision, error) {
	result, err := driver.Query(ctx, `
        SELECT group_id, fact_key, count() AS count
        FROM relates_to
        WHERE fact_key != "" AND invalid_at IS NONE
        GROUP BY group_id, fact_key;
    `, nil)
	if err != nil {
		return nil, err
	}
	var collisions []FactKeyCollision
	for _, group := range UnwrapRows(result) {
		if intFromAny(group["count"]) < 2 {
			continue
		}
		groupID := stringFromAny(group["group_id"])
		factKey := stringFromAny(group["fact_key"])
		rows, err := driver.Query(ctx, `
            SELECT * FROM relates_to
            WHERE group_id = $group_id AND fact_key = $fact_key AND invalid_at IS NONE
            ORDER BY created_at ASC;
        `, map[string]any{"group_id": groupID, "fact_key": factKey})
		if err != nil {
			return nil, err
		}
		collisions = append(collisions, FactKeyCollision{GroupID: groupID, FactKey: factKey, Edges: UnwrapRows(rows)})
	}
	return collisions, nil
}

func ReconcileFactKeyCollisions(ctx context.Context, driver Queryer, policy ReconciliationPolicy) (fixed, skipped []FactKeyCollision, err error) {
	if policy != ReconcileMergeIdentical && policy != ReconcileMergeByFactKey {
		return nil, nil, fmt.Errorf("policy must be %q or %q", ReconcileMergeIdentical, ReconcileMergeByFactKey)
	}
	collisions, err := InspectFactKeyCollisions(ctx, driver)
	if err != nil {
		return nil, nil, err
	}
	for _, collision := range collisions {
		if len(collision.Edges) < 2 {
			continue
		}
		winnerIndex := 0
		if policy == ReconcileMergeByFactKey {
			winnerIndex = canonicalCollisionEdgeIndex(collision.Edges)
			if winnerIndex < 0 {
				continue
			}
		}
		winner := collision.Edges[winnerIndex]
		duplicates := make([]map[string]any, 0, len(collision.Edges)-1)
		for i, edge := range collision.Edges {
			if i != winnerIndex {
				duplicates = append(duplicates, edge)
			}
		}
		if policy == ReconcileMergeIdentical {
			wp := semanticPayload(winner)
			conflict := false
			for _, edge := range duplicates {
				if !reflect.DeepEqual(semanticPayload(edge), wp) {
					conflict = true
					break
				}
			}
			if conflict {
				skipped = append(skipped, collision)
				continue
			}
		}

		episodeSet := map[string]struct{}{}
		for _, edge := range collision.Edges {
			for _, ep := range asStringSlice(edge["episodes"]) {
				if ep != "" {
					episodeSet[ep] = struct{}{}
				}
			}
		}
		episodes := make([]string, 0, len(episodeSet))
		for ep := range episodeSet {
			episodes = append(episodes, ep)
		}
		sort.Strings(episodes)
		winnerUUID := stringFromAny(winner["uuid"])
		if _, err := driver.Query(ctx,
			"UPDATE relates_to SET episodes = $episodes WHERE uuid = $uuid;",
			map[string]any{"episodes": episodes, "uuid": winnerUUID},
		); err != nil {
			return fixed, skipped, err
		}

		for _, duplicate := range duplicates {
			duplicateUUID := stringFromAny(duplicate["uuid"])
			legacyKey := collision.FactKey + "::legacy-duplicate::" + duplicateUUID
			if _, err := driver.Query(ctx, `
                UPDATE relates_to SET
                    fact_key = $legacy_key,
                    status = "superseded",
                    superseded_by = $winner_uuid
                WHERE uuid = $uuid;
                `, map[string]any{
				"legacy_key":  legacyKey,
				"winner_uuid": winnerUUID,
				"uuid":        duplicateUUID,
			}); err != nil {
				return fixed, skipped, err
			}
		}
		fixed = append(fixed, collision)
	}
	return fixed, skipped, nil
}

func isEmptyString(v any) bool { return strings.TrimSpace(stringFromAny(v)) == "" }
