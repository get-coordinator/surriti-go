package surriti

import (
	"context"
	"fmt"
	"sort"
	"time"
)

func UpsertSyntheticEntity(ctx context.Context, driver Queryer, groupID, name, summary, label string, now time.Time) (string, error) {
	if now.IsZero() {
		now = utcNow()
	}
	raw, err := driver.Query(ctx, "SELECT uuid FROM entity WHERE group_id = $g AND name = $n LIMIT 1;", map[string]any{"g": groupID, "n": name})
	if err != nil {
		return "", err
	}
	rows := UnwrapRows(raw)
	if len(rows) > 0 {
		u := stringFromAny(rows[0]["uuid"])
		_, err = driver.Query(ctx, "UPDATE entity SET summary = $s, last_seen_at = $t WHERE uuid = $u;", map[string]any{"u": u, "s": summary, "t": now})
		return u, err
	}
	u := newUUID()
	_, err = driver.Query(ctx, `
CREATE entity CONTENT {
 uuid: $u, group_id: $g, name: $n, summary: $s,
 labels: ['Entity', $l], attributes: {},
 created_at: $t, last_seen_at: $t,
 aliases: [], traits: [], goals_active: []
};`, map[string]any{"u": u, "g": groupID, "n": name, "s": summary, "l": label, "t": now})
	return u, err
}

func UpsertSyntheticEdge(ctx context.Context, driver Queryer, embedder Embedder, groupID, subjectUUID, objectUUID, predicate, factText, memoryClass string, confidence float64, supporting, consolidates []string, stability string, isBelief bool, beliefHolder *string, extraAttrs map[string]any, factKeyQualifier string, now time.Time) (string, error) {
	if now.IsZero() {
		now = utcNow()
	}
	if stability == "" {
		stability = "reinforced"
	}
	factKey := MakeFactKey(groupID, subjectUUID, predicate, objectUUID, factKeyQualifier)
	var emb []float64
	if embedder != nil {
		if v, err := embedder.Create(ctx, factText); err == nil {
			emb = v
		}
	}
	supporting = sortedStringsUnique(supporting)
	consolidates = sortedStringsUnique(consolidates)
	sort.Strings(supporting)
	sort.Strings(consolidates)
	attrs := map[string]any{"memory_class": memoryClass, "supporting_edges": supporting}
	for k, v := range extraAttrs {
		attrs[k] = v
	}
	raw, err := driver.Query(ctx, "SELECT uuid FROM relates_to WHERE group_id = $g AND fact_key = $k LIMIT 1;", map[string]any{"g": groupID, "k": factKey})
	if err != nil {
		return "", err
	}
	rows := UnwrapRows(raw)
	if len(rows) > 0 {
		u := stringFromAny(rows[0]["uuid"])
		_, err = driver.Query(ctx, `
UPDATE relates_to SET
 fact = $f, confidence = $c, last_reinforced_at = $t,
 consolidates = $cons,
 attributes = object::extend(attributes, $attrs)
WHERE uuid = $u;`, map[string]any{"u": u, "f": factText, "c": confidence, "t": now, "cons": consolidates, "attrs": attrs})
		return u, err
	}
	u := newUUID()
	var derivedFrom *string
	if len(supporting) > 0 {
		v := supporting[0]
		derivedFrom = &v
	}
	_, err = driver.Query(ctx, `
RELATE (type::record("entity", $su))->relates_to->(type::record("entity", $tu))
CONTENT {
 uuid: $u, group_id: $g, name: $p, canonical_name: $p,
 fact: $f, fact_embedding: $emb,
 episodes: [], valid_at: $t, created_at: $t,
 last_reinforced_at: $t, attributes: $attrs,
 status: 'active', polarity: 'positive', source_type: 'system',
 confidence: $c, temporal: false, singleton: false,
 derived: true, derived_from: $df, fact_key: $k,
 weight: 1.0, reinforcement_count: 1, decay_score: 1.0,
 stability: $stab, consolidates: $cons,
 is_belief: $belief, belief_holder: $bh
};`, map[string]any{"su": subjectUUID, "tu": objectUUID, "u": u, "g": groupID, "p": predicate, "f": factText, "emb": emb, "t": now, "attrs": attrs, "c": confidence, "k": factKey, "df": derivedFrom, "stab": stability, "cons": consolidates, "belief": isBelief, "bh": beliefHolder})
	return u, err
}

func CacheOnSubject(ctx context.Context, driver Queryer, subjectUUID, field, value string) error {
	if field != "traits" && field != "goals_active" {
		return fmt.Errorf("unsupported subject cache field: %q", field)
	}
	q := fmt.Sprintf("UPDATE entity SET %s = array::distinct(array::concat(%s, [$v])) WHERE uuid = $u;", field, field)
	_, err := driver.Query(ctx, q, map[string]any{"u": subjectUUID, "v": value})
	return err
}
