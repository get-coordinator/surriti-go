package surriti

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var selfSlugRE = regexp.MustCompile("[^a-z0-9]+")

func selfSlug(value string) string {
	s := strings.Trim(selfSlugRE.ReplaceAllString(casefold(value), "_"), "_")
	if s == "" {
		s = "self_model"
	}
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func stableSelfID(prefix, groupID, value string) string {
	sum := sha1.Sum([]byte(groupID + "\x00" + value))
	return prefix + "_" + selfSlug(value) + "_" + hex.EncodeToString(sum[:])[:12]
}

func querySelfEpisodes(ctx context.Context, driver Queryer, groupID string, episodeUUIDs []string) ([]map[string]any, error) {
	if len(episodeUUIDs) > 0 {
		raw, err := driver.Query(ctx, `
SELECT name, content, source, source_description, reference_time, created_at, group_id
FROM episode
WHERE group_id = $group_id
 AND uuid IN $episode_uuids
 AND source CONTAINS 'self_'
ORDER BY created_at DESC
LIMIT 100;`, map[string]any{"group_id": groupID, "episode_uuids": episodeUUIDs})
		if err != nil {
			return nil, err
		}
		rows := UnwrapRows(raw)
		if len(rows) > 0 {
			return rows, nil
		}
	}
	raw, err := driver.Query(ctx, `
SELECT name, content, source, source_description, reference_time, created_at, group_id
FROM episode
WHERE group_id = $group_id
 AND source CONTAINS 'self_'
ORDER BY created_at DESC
LIMIT 100;`, map[string]any{"group_id": groupID})
	if err != nil {
		return nil, err
	}
	return UnwrapRows(raw), nil
}

func getSelfEntityRow(ctx context.Context, driver Queryer, groupID string) (map[string]any, error) {
	name := "assistant"
	if groupID != "" {
		name = "assistant_" + groupID
	}
	raw, err := driver.Query(ctx, "SELECT * FROM entity WHERE group_id = $group_id AND name = $name LIMIT 1;", map[string]any{"group_id": groupID, "name": name})
	if err != nil {
		return nil, err
	}
	rows := UnwrapRows(raw)
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func upsertSelfModelEntity(ctx context.Context, driver Queryer, uuid, groupID, name, summary string, labels []string) (string, error) {
	raw, err := driver.Query(ctx, "SELECT * FROM entity WHERE group_id = $group_id AND uuid = $uuid LIMIT 1;", map[string]any{"group_id": groupID, "uuid": uuid})
	if err != nil {
		return "", err
	}
	payload := map[string]any{"uuid": uuid, "group_id": groupID, "name": name, "summary": summary, "labels": labels, "created_at": utcNow()}
	if len(UnwrapRows(raw)) > 0 {
		_, err = driver.Query(ctx, `UPDATE type::record("entity", $uuid) SET summary = $summary, labels = $labels;`, payload)
		return uuid, err
	}
	_, err = driver.Query(ctx, `
CREATE type::record("entity", $uuid) CONTENT {
 uuid: $uuid, group_id: $group_id, name: $name, summary: $summary,
 labels: $labels, attributes: {}, created_at: $created_at
};`, payload)
	if err == nil {
		return uuid, nil
	}
	if !strings.Contains(err.Error(), "entity_name_uniq") {
		return "", err
	}
	fallback, qerr := driver.Query(ctx, "SELECT * FROM entity WHERE group_id = $group_id AND name = $name LIMIT 1;", map[string]any{"group_id": groupID, "name": name})
	if qerr != nil {
		return "", qerr
	}
	rows := UnwrapRows(fallback)
	if len(rows) == 0 {
		return "", err
	}
	actual := stringFromAny(rows[0]["uuid"])
	payload["uuid"] = actual
	_, qerr = driver.Query(ctx, `UPDATE type::record("entity", $uuid) SET summary = $summary, labels = $labels;`, payload)
	return actual, qerr
}

func upsertSelfModelEdge(ctx context.Context, driver Queryer, groupID, selfUUID, targetUUID, edgeUUID, predicate, fact string, confidence float64, isBelief bool) error {
	raw, err := driver.Query(ctx, "SELECT * FROM relates_to WHERE group_id = $group_id AND uuid = $uuid LIMIT 1;", map[string]any{"group_id": groupID, "uuid": edgeUUID})
	if err != nil {
		return err
	}
	payload := map[string]any{"src": selfUUID, "tgt": targetUUID, "uuid": edgeUUID, "group_id": groupID, "name": predicate, "fact": fact, "confidence": confidence, "is_belief": isBelief, "status": "active", "source_type": "assistant", "attributes": map[string]any{"memory_class": "self_model"}, "fact_key": MakeFactKey(groupID, selfUUID, predicate, targetUUID, ""), "created_at": utcNow()}
	if len(UnwrapRows(raw)) > 0 {
		_, err = driver.Query(ctx, `
UPDATE relates_to SET fact=$fact, confidence=$confidence, is_belief=$is_belief,
 status="active", invalid_at=NONE, fact_key=$fact_key, attributes=$attributes
WHERE group_id=$group_id AND uuid=$uuid;`, payload)
		return err
	}
	_, err = driver.Query(ctx, `
RELATE (type::record("entity", $src))->relates_to->(type::record("entity", $tgt))
CONTENT {
 uuid:$uuid, group_id:$group_id, name:$name, fact:$fact,
 confidence:$confidence, is_belief:$is_belief, status:$status,
 source_type:$source_type, fact_key:$fact_key, attributes:$attributes,
 episodes:[], reinforcement_count:1, recall_count:0, decay_score:1.0,
 stability:"persistent", created_at:$created_at
};`, payload)
	return err
}

func renderSelfEpisodes(episodes []map[string]any) string {
	n := len(episodes)
	if n > 12 {
		n = 12
	}
	lines := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ep := episodes[i]
		src := stringFromAny(ep["source_description"])
		if src == "" {
			src = stringFromAny(ep["source"])
		}
		raw := stringFromAny(ep["content"])
		var data map[string]any
		_ = json.Unmarshal([]byte(raw), &data)
		kind := stringFromAny(data["kind"])
		if data != nil && (kind == "reflective_self_observation" || kind == "interaction_event") {
			parts := []string{"[" + src + "] kind=" + kind}
			if summary := stringFromAny(data["interaction_summary"]); summary != "" {
				parts = append(parts, "  summary: "+summary)
			}
			if q := mapFromAny(data["perceived_interaction_quality"]); len(q) > 0 {
				parts = append(parts, fmt.Sprintf("  quality: %v — %s", q["score"], stringFromAny(q["reason"])))
			}
			if signals := mapFromAny(data["observable_signals"]); len(signals) > 0 {
				feedback := asStringSlice(signals["feedback"])
				if len(feedback) > 0 {
					parts = append(parts, "  signals: "+strings.Join(feedback, ", "))
				}
			}
			for _, rawLC := range asAnySlice(data["lesson_candidates"]) {
				lc := mapFromAny(rawLC)
				lesson := stringFromAny(lc["lesson"])
				if lesson == "" {
					continue
				}
				conf := llmFloat(lc["confidence"], 0)
				parts = append(parts, fmt.Sprintf("  lesson (conf=%.2f): %s", conf, lesson))
				if ev := stringFromAny(lc["evidence"]); ev != "" {
					parts = append(parts, "    evidence: "+ev)
				}
			}
			for _, adj := range asStringSlice(data["future_behavior_adjustments"]) {
				parts = append(parts, "  adjustment: "+adj)
			}
			lines = append(lines, strings.Join(parts, "\n"))
			continue
		}
		content := raw
		if len([]rune(content)) > 600 {
			content = prefixRunes(content, 600) + "…"
		}
		lines = append(lines, "["+src+"] "+content)
	}
	if len(lines) == 0 {
		return "(no episodes)"
	}
	return strings.Join(lines, "\n\n")
}

func synthesizeSelfJSON(ctx context.Context, llm LLMClient, system, prompt string) (map[string]any, error) {
	synth, ok := llm.(Synthesizer)
	if !ok {
		return nil, nil
	}
	raw, err := synth.Synthesize(ctx, system, prompt)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stripJSONFences(raw)), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func selfObservationPrompt(episodes []map[string]any) string {
	return "Analyze these AI assistant self-observations and extract durable behavioral traits and beliefs.\n" +
		"Episodes may contain structured JSON (reflective_self_observation or interaction_event).\n" +
		"Prefer lesson_candidates from structured entries; use evidence field for confidence.\n\n" +
		"Self-observations:\n" + renderSelfEpisodes(episodes) + "\n\n" +
		"Return JSON:\n{\n  \"traits\": [\n    {\"trait\": \"<concise label>\", \"evidence\": \"<direct quote or signal>\", \"confidence\": 0.0, \"support_count\": 1}\n  ],\n  \"beliefs\": [\n    {\"belief\": \"<first-person operational belief about behavior>\", \"confidence\": 0.0, \"evidence\": \"<direct evidence>\"}\n  ]\n}\n\n" +
		"Rules: confidence >= 0.5 required. No private feelings, no hidden motives. Operational only.\n"
}

func selfPatternPrompt(episodes []map[string]any) string {
	return "Identify recurring behavioral patterns from these AI assistant self-observations.\n" +
		"Prefer future_behavior_adjustments from structured reflective_self_observation entries.\n" +
		"Only include patterns appearing in >= 2 episodes or explicitly labeled recurring.\n\n" +
		"Observations:\n" + renderSelfEpisodes(episodes) + "\n\n" +
		"Return JSON:\n{\n  \"patterns\": [\n    {\"pattern\": \"<description>\", \"frequency\": \"occasional|recurring|frequent\", \"context\": \"<domain>\"}\n  ]\n}\n"
}

func selfEventPrompt(episodes []map[string]any) string {
	return "Analyze these AI assistant correction and success events. Extract durable operational lessons.\n" +
		"For corrections: what went wrong, what adjustment is needed.\n" +
		"For successes: what worked, what to reinforce.\n" +
		"Prefer lesson_candidates and future_behavior_adjustments from structured JSON entries.\n\n" +
		"Events:\n" + renderSelfEpisodes(episodes) + "\n\n" +
		"Return JSON:\n{\n  \"traits\": [\n    {\"trait\": \"<label>\", \"evidence\": \"<quote>\", \"confidence\": 0.0, \"support_count\": 1}\n  ],\n  \"beliefs\": [\n    {\"belief\": \"<first-person operational belief>\", \"confidence\": 0.0, \"evidence\": \"<evidence>\"}\n  ]\n}\n\n" +
		"Rules: confidence >= 0.5. Beliefs must reference this user's specific interaction patterns.\n"
}

func writeSelfTrait(ctx context.Context, driver Queryer, groupID string, self map[string]any, item map[string]any) (bool, error) {
	name := stringFromAny(item["trait"])
	if name == "" {
		return false, nil
	}
	conf := llmFloat(item["confidence"], .5)
	if conf < .5 {
		return false, nil
	}
	u := stableSelfID("trait", groupID, name)
	evidence := stringFromAny(item["evidence"])
	summary := evidence
	if summary == "" {
		summary = "Self-trait: " + name
	}
	support := intFromAny(item["support_count"])
	if support == 0 {
		support = 1
	}
	if support > 1 {
		summary = fmt.Sprintf("%s (seen %dx)", summary, support)
	}
	actual, err := upsertSelfModelEntity(ctx, driver, u, groupID, name, summary, []string{"SelfTrait", "Trait"})
	if err != nil {
		return false, err
	}
	err = upsertSelfModelEdge(ctx, driver, groupID, stringFromAny(self["uuid"]), actual, "edge_"+actual, "has_trait", "has_trait: "+name, conf, false)
	return err == nil, err
}

func writeSelfBelief(ctx context.Context, driver Queryer, groupID string, self map[string]any, item map[string]any) (bool, error) {
	text := stringFromAny(item["belief"])
	if text == "" {
		return false, nil
	}
	conf := llmFloat(item["confidence"], .5)
	if conf < .5 {
		return false, nil
	}
	u := stableSelfID("belief", groupID, text)
	summary := text
	if ev := stringFromAny(item["evidence"]); ev != "" {
		if len(ev) > 200 {
			ev = prefixRunes(ev, 200)
		}
		summary = text + " [evidence: " + ev + "]"
	}
	actual, err := upsertSelfModelEntity(ctx, driver, u, groupID, "self_belief", summary, []string{"SelfBelief"})
	if err != nil {
		return false, err
	}
	err = upsertSelfModelEdge(ctx, driver, groupID, stringFromAny(self["uuid"]), actual, "edge_"+actual, "has_belief", text, conf, true)
	return err == nil, err
}

func writeSelfPattern(ctx context.Context, driver Queryer, groupID string, self map[string]any, item map[string]any) (bool, error) {
	name := stringFromAny(item["pattern"])
	if name == "" {
		return false, nil
	}
	parts := []string{name}
	if v := stringFromAny(item["frequency"]); v != "" {
		parts = append(parts, "frequency="+v)
	}
	if v := stringFromAny(item["context"]); v != "" {
		parts = append(parts, "context="+v)
	}
	u := stableSelfID("pattern", groupID, name)
	actual, err := upsertSelfModelEntity(ctx, driver, u, groupID, name, strings.Join(parts, " | "), []string{"SelfPattern", "Pattern"})
	if err != nil {
		return false, err
	}
	err = upsertSelfModelEdge(ctx, driver, groupID, stringFromAny(self["uuid"]), actual, "edge_"+actual, "has_pattern", "has_pattern: "+name, .7, false)
	return err == nil, err
}

func persistSelfTraitsBeliefs(ctx context.Context, driver Queryer, groupID string, self map[string]any, data map[string]any) (int, int, error) {
	traits := []map[string]any{}
	beliefs := []map[string]any{}
	for _, raw := range asAnySlice(data["traits"]) {
		m := mapFromAny(raw)
		if llmFloat(m["confidence"], 0) >= .5 {
			traits = append(traits, m)
		}
	}
	for _, raw := range asAnySlice(data["beliefs"]) {
		m := mapFromAny(raw)
		if llmFloat(m["confidence"], 0) >= .5 {
			beliefs = append(beliefs, m)
		}
	}
	for _, m := range traits {
		if _, err := writeSelfTrait(ctx, driver, groupID, self, m); err != nil {
			return 0, 0, err
		}
	}
	for _, m := range beliefs {
		if _, err := writeSelfBelief(ctx, driver, groupID, self, m); err != nil {
			return 0, 0, err
		}
	}
	return len(traits), len(beliefs), nil
}

func RunSelfAwarenessPass(ctx context.Context, driver Queryer, llm LLMClient, groupID string, episodeUUIDs []string, config CognitionConfig) (map[string]int, error) {
	_ = config
	metrics := map[string]int{"self_episodes_read": 0, "self_traits_extracted": 0, "self_beliefs_extracted": 0, "self_patterns_detected": 0}
	// Python intentionally swallows every failure inside this pass so a
	// self-model issue never marks the entire cognition batch failed.
	episodes, err := querySelfEpisodes(ctx, driver, groupID, episodeUUIDs)
	if err != nil {
		return metrics, nil
	}
	metrics["self_episodes_read"] = len(episodes)
	if len(episodes) == 0 {
		return metrics, nil
	}
	self, err := getSelfEntityRow(ctx, driver, groupID)
	if err != nil || self == nil {
		return metrics, nil
	}
	byType := map[string][]map[string]any{}
	typeOrder := []string{}
	for _, ep := range episodes {
		src := stringFromAny(ep["source"])
		if strings.HasPrefix(src, "self_") {
			if _, ok := byType[src]; !ok {
				typeOrder = append(typeOrder, src)
			}
			byType[src] = append(byType[src], ep)
		}
	}
	for _, typ := range typeOrder {
		eps := byType[typ]
		switch typ {
		case string(EpisodeSelfObservation):
			data, e := synthesizeSelfJSON(ctx, llm, "Extract structured self-model data from AI self-observations. Return only valid JSON.", selfObservationPrompt(eps))
			if e != nil || data == nil {
				continue
			}
			traits, beliefs, e := persistSelfTraitsBeliefs(ctx, driver, groupID, self, data)
			if e != nil {
				continue
			}
			metrics["self_traits_extracted"] = traits
			metrics["self_beliefs_extracted"] = beliefs
		case string(EpisodeSelfPattern):
			data, e := synthesizeSelfJSON(ctx, llm, "Extract recurring behavioral patterns. Return only valid JSON.", selfPatternPrompt(eps))
			if e != nil || data == nil {
				continue
			}
			patterns := asAnySlice(data["patterns"])
			written := 0
			failed := false
			for _, raw := range patterns {
				m := mapFromAny(raw)
				ok, e := writeSelfPattern(ctx, driver, groupID, self, m)
				if e != nil {
					failed = true
					break
				}
				if ok {
					written++
				}
			}
			if !failed {
				metrics["self_patterns_detected"] = written
			}
		case string(EpisodeSelfCorrection), string(EpisodeSelfSuccess):
			data, e := synthesizeSelfJSON(ctx, llm, "Extract operational lessons from assistant events. Return only valid JSON.", selfEventPrompt(eps))
			if e != nil || data == nil {
				continue
			}
			traits, beliefs, e := persistSelfTraitsBeliefs(ctx, driver, groupID, self, data)
			if e != nil {
				continue
			}
			metrics["self_traits_extracted"] += traits
			metrics["self_beliefs_extracted"] += beliefs
		}
	}
	return metrics, nil
}
