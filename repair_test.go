package surriti

import (
	"context"
	"strings"
	"testing"
)

type repairWrite struct {
	q    string
	vars map[string]any
}
type collisionDriver struct {
	edges  []map[string]any
	writes []repairWrite
}

func (d *collisionDriver) Query(_ context.Context, q string, vars map[string]any) (any, error) {
	if strings.Contains(q, "GROUP BY group_id, fact_key") {
		return []any{[]map[string]any{{"group_id": "user:1", "fact_key": "key", "count": len(d.edges)}}}, nil
	}
	if strings.Contains(q, "SELECT * FROM relates_to") {
		return []any{d.edges}, nil
	}
	d.writes = append(d.writes, repairWrite{q: q, vars: vars})
	return []any{[]map[string]any{}}, nil
}

func TestInspectionIsReadOnlyAndReturnsProvenanceRows(t *testing.T) {
	d := &collisionDriver{edges: []map[string]any{{"uuid": "old", "episodes": []string{"ep-1"}}, {"uuid": "new", "episodes": []string{"ep-2"}}}}
	c, err := InspectFactKeyCollisions(context.Background(), d)
	if err != nil { t.Fatal(err) }
	if len(c) != 1 || len(c[0].Edges) != 2 || c[0].Edges[0]["uuid"] != "old" || len(d.writes) != 0 {
		t.Fatalf("c=%v writes=%v", c, d.writes)
	}
}

func TestRepairMergesOnlyIdenticalClaimsAndUnionsEpisodes(t *testing.T) {
	d := &collisionDriver{edges: []map[string]any{
		{"uuid": "old", "name": "likes", "fact": "A likes B", "episodes": []string{"ep-1"}},
		{"uuid": "new", "name": "likes", "fact": "A likes B", "episodes": []string{"ep-2", "ep-1"}},
	}}
	fixed, skipped, err := ReconcileFactKeyCollisions(context.Background(), d, ReconcileMergeIdentical)
	if err != nil { t.Fatal(err) }
	if len(fixed) != 1 || len(skipped) != 0 || len(d.writes) != 2 {
		t.Fatalf("fixed=%d skipped=%d writes=%d", len(fixed), len(skipped), len(d.writes))
	}
	if d.writes[0].vars["uuid"] != "old" { t.Fatalf("winner=%v", d.writes[0].vars) }
	eps := d.writes[0].vars["episodes"].([]string)
	if !equalStrings(eps, []string{"ep-1", "ep-2"}) { t.Fatalf("episodes=%v", eps) }
	if d.writes[1].vars["legacy_key"] != "key::legacy-duplicate::new" || d.writes[1].vars["winner_uuid"] != "old" {
		t.Fatalf("vars=%v", d.writes[1].vars)
	}
}

func TestRepairLeavesConflictingClaimMetadataForOperator(t *testing.T) {
	d := &collisionDriver{edges: []map[string]any{
		{"uuid": "old", "name": "likes", "fact": "A likes B", "status": "active", "episodes": []string{}},
		{"uuid": "new", "name": "likes", "fact": "A likes B", "status": "superseded", "episodes": []string{}},
	}}
	fixed, skipped, err := ReconcileFactKeyCollisions(context.Background(), d, ReconcileMergeIdentical)
	if err != nil { t.Fatal(err) }
	if len(fixed) != 0 || len(skipped) != 1 || len(d.writes) != 0 {
		t.Fatalf("fixed=%d skipped=%d writes=%d", len(fixed), len(skipped), len(d.writes))
	}
}

func TestStartupPolicyPrefersActiveCanonicalRow(t *testing.T) {
	d := &collisionDriver{edges: []map[string]any{
		{"uuid": "old", "name": "training_for", "fact": "Training for marathon", "status": "superseded", "episodes": []string{"ep-1"}},
		{"uuid": "new", "name": "training_for", "fact": "Preparing for a marathon", "status": "active", "episodes": []string{"ep-2"}},
	}}
	fixed, skipped, err := ReconcileFactKeyCollisions(context.Background(), d, ReconcileMergeByFactKey)
	if err != nil { t.Fatal(err) }
	if len(fixed) != 1 || len(skipped) != 0 { t.Fatalf("fixed=%d skipped=%d", len(fixed), len(skipped)) }
	if d.writes[0].vars["uuid"] != "new" || d.writes[1].vars["uuid"] != "old" || d.writes[1].vars["winner_uuid"] != "new" {
		t.Fatalf("writes=%v", d.writes)
	}
}
