package surriti

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func ConsolidateEdges(ctx context.Context, driver Queryer, embedder Embedder, groupID string, threshold int, minSpanDays float64) (int, error) {
	if threshold <= 0 {
		threshold = 8
	}
	raw, err := driver.Query(ctx, `
SELECT *, record::id(in) AS source_node_uuid, record::id(out) AS target_node_uuid
FROM relates_to
WHERE group_id = $g
 AND status = 'active'
 AND stability != 'consolidated'
 AND fact_key != '';`, map[string]any{"g": groupID})
	if err != nil {
		return 0, err
	}
	rows := UnwrapRows(raw)
	if len(rows) == 0 {
		return 0, nil
	}
	edges := []EntityEdge{}
	buckets := map[string][]EntityEdge{}
	epSet := map[string]struct{}{}
	for _, r := range rows {
		e := ParseEdge(r)
		edges = append(edges, e)
		if e.FactKey != "" {
			buckets[e.FactKey] = append(buckets[e.FactKey], e)
		}
		for _, ep := range e.Episodes {
			if ep != "" {
				epSet[ep] = struct{}{}
			}
		}
	}
	epTimes := map[string]time.Time{}
	if len(epSet) > 0 {
		ids := []string{}
		for u := range epSet {
			ids = append(ids, u)
		}
		eraw, e := driver.Query(ctx, "SELECT uuid, reference_time FROM episode WHERE uuid IN $u;", map[string]any{"u": ids})
		if e != nil {
			return 0, e
		}
		for _, r := range UnwrapRows(eraw) {
			if t := coerceTime(r["reference_time"]); t != nil {
				epTimes[stringFromAny(r["uuid"])] = *t
			}
		}
	}
	written := 0
	now := utcNow()
	for key, bucket := range buckets {
		all := map[string]struct{}{}
		for _, e := range bucket {
			for _, ep := range e.Episodes {
				all[ep] = struct{}{}
			}
		}
		if len(all) < threshold {
			continue
		}
		ts := []time.Time{}
		for ep := range all {
			if t, ok := epTimes[ep]; ok {
				ts = append(ts, t)
			}
		}
		if len(ts) < 2 {
			continue
		}
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
		if ts[len(ts)-1].Sub(ts[0]).Hours()/24 < minSpanDays {
			continue
		}
		canon := bucket[0]
		sum := 0.0
		for _, e := range bucket {
			sum += e.Confidence
			if e.Confidence > canon.Confidence {
				canon = e
			}
		}
		conf := sum/float64(len(bucket)) + .1
		if conf > 1 {
			conf = 1
		}
		support := []string{}
		for _, e := range bucket {
			support = append(support, e.UUID)
		}
		predicate := canon.CanonicalName
		if predicate == "" {
			predicate = canon.Name
		}
		fact := canon.Fact
		if fact == "" {
			fact = canon.Name + " (consolidated)"
		}
		_, err := UpsertSyntheticEdge(ctx, driver, embedder, groupID, canon.SourceNodeUUID, canon.TargetNodeUUID, predicate, fact, "consolidated", conf, support, support, "consolidated", false, nil, map[string]any{"consolidated_from_key": key, "support_count": len(all)}, "consolidated", now)
		if err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

func stagnantSummary(edges []EntityEdge) string {
	facts := []string{}
	for _, e := range edges {
		v := strings.TrimSpace(e.Fact)
		if v == "" {
			v = strings.TrimSpace(e.Name)
		}
		if v != "" {
			facts = append(facts, v)
		}
	}
	if len(facts) == 0 {
		return "Low-vitality memory cluster."
	}
	n := len(facts)
	if n > 5 {
		n = 5
	}
	text := strings.Join(facts[:n], "; ")
	if len(facts) > 5 {
		text += fmt.Sprintf("; and %d related older facts", len(facts)-5)
	}
	return "Low-vitality memory cluster: " + text
}

func ConsolidateStagnantEdges(ctx context.Context, driver Queryer, embedder Embedder, groupID string, minEdges, maxEdges int) (int, error) {
	if minEdges <= 0 {
		minEdges = 5
	}
	if maxEdges <= 0 {
		maxEdges = 120
	}
	raw, err := driver.Query(ctx, `
SELECT *, record::id(in) AS source_node_uuid, record::id(out) AS target_node_uuid
FROM relates_to
WHERE group_id = $g AND status = 'active' AND stability != 'consolidated'
LIMIT $limit;`, map[string]any{"g": groupID, "limit": maxEdges})
	if err != nil {
		return 0, err
	}
	now := utcNow()
	buckets := map[string][]EntityEdge{}
	for _, r := range UnwrapRows(raw) {
		e := ParseEdge(r)
		if IsDecayProtected(e) || EffectiveConfidence(e, now, nil) > 0 {
			continue
		}
		bucket := ""
		if e.Domain != nil {
			bucket = *e.Domain
		}
		if bucket == "" {
			bucket = e.CanonicalName
		}
		if bucket == "" {
			bucket = e.Name
		}
		if bucket == "" {
			bucket = "misc"
		}
		key := e.SourceNodeUUID + "\x00" + e.MemoryClass + "\x00" + bucket
		buckets[key] = append(buckets[key], e)
	}
	written := 0
	for key, bucket := range buckets {
		if len(bucket) < minEdges {
			continue
		}
		parts := strings.SplitN(key, "\x00", 3)
		canon := bucket[0]
		support := []string{}
		for _, e := range bucket {
			support = append(support, e.UUID)
		}
		predicate := "archived_summary_" + parts[2]
		_, err := UpsertSyntheticEdge(ctx, driver, embedder, groupID, canon.SourceNodeUUID, canon.TargetNodeUUID, predicate, stagnantSummary(bucket), "archived_summary", .5, support, support, "consolidated", false, nil, map[string]any{"summary_type": "stagnant", "source_memory_class": parts[1], "source_count": len(bucket), "lossy": true}, "archived::"+parts[2], now)
		if err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
