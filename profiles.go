package surriti

import (
	"context"
	"strings"
	"unicode/utf8"
)

type EntityProfileSummarizer interface {
	SummarizeEntityProfile(context.Context, string, []string, int) (string, error)
}

func ComposeProfileSummary(entityName string, facts []string, maxChars int) string {
	if maxChars == 0 {
		maxChars = 800
	}
	if len(facts) == 0 {
		return ""
	}
	bullets := []string{}
	used := 0
	for _, fact := range facts {
		line := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(fact), "."))
		if line == "" {
			continue
		}
		candidate := line
		if len(bullets) > 0 {
			candidate = "; " + line
		}
		if used+utf8.RuneCountInString(candidate) > maxChars {
			break
		}
		bullets = append(bullets, line)
		used += utf8.RuneCountInString(candidate)
	}
	return entityName + ": " + strings.Join(bullets, "; ") + "."
}

func summarizeProfile(ctx context.Context, llm LLMClient, entityName string, facts []string, maxChars int) string {
	if hook, ok := llm.(EntityProfileSummarizer); ok {
		if summary, err := hook.SummarizeEntityProfile(ctx, entityName, facts, maxChars); err == nil && summary != "" {
			return summary
		}
	}
	return ComposeProfileSummary(entityName, facts, maxChars)
}

func RefreshEntityProfiles(ctx context.Context, driver Queryer, embedder Embedder, llm LLMClient, groupID string, entityUUIDs []string, maxFacts, maxChars int) int {
	if len(entityUUIDs) == 0 {
		return 0
	}
	if maxFacts == 0 {
		maxFacts = 30
	}
	if maxChars == 0 {
		maxChars = 800
	}
	refreshed := 0
	for _, uuid := range entityUUIDs {
		if ctx.Err() != nil {
			break
		}
		func() {
			raw, err := driver.Query(ctx, "SELECT * FROM entity WHERE group_id = $g AND uuid = $u LIMIT 1;", map[string]any{"g": groupID, "u": uuid})
			if err != nil {
				return
			}
			rows := UnwrapRows(raw)
			if len(rows) == 0 {
				return
			}
			entity := ParseEntity(rows[0])
			raw, err = driver.Query(ctx, `
SELECT * FROM relates_to WHERE group_id = $g
AND (in = $rec OR out = $rec)
AND invalid_at IS NONE
ORDER BY valid_at DESC LIMIT $lim;`, map[string]any{"g": groupID, "rec": "entity:" + uuid, "lim": maxFacts})
			if err != nil {
				return
			}
			facts := []string{}
			for _, row := range UnwrapRows(raw) {
				edge := ParseEdge(row)
				if edge.Fact != "" {
					facts = append(facts, edge.Fact)
				}
			}
			display := entity.Name
			if entity.CanonicalName != nil && *entity.CanonicalName != "" {
				display = *entity.CanonicalName
			}
			sidecarIDs := orderedUniqueStrings(append(append([]string(nil), entity.Traits...), entity.GoalsActive...))
			traitNames := []string{}
			goalNames := []string{}
			if len(sidecarIDs) > 0 {
				if sideRaw, qerr := driver.Query(ctx, "SELECT uuid, name, labels FROM entity WHERE group_id = $g AND uuid IN $u;", map[string]any{"g": groupID, "u": sidecarIDs}); qerr == nil {
					for _, sr := range UnwrapRows(sideRaw) {
						name := strings.TrimSpace(stringFromAny(sr["name"]))
						if name == "" {
							continue
						}
						labels := asStringSlice(sr["labels"])
						if containsString(labels, "trait") {
							traitNames = append(traitNames, name)
						} else if containsString(labels, "goal") {
							goalNames = append(goalNames, name)
						}
					}
				}
			}
			summary := summarizeProfile(ctx, llm, display, facts, maxChars)
			tails := []string{}
			if len(traitNames) > 0 {
				n := len(traitNames)
				if n > 6 {
					n = 6
				}
				tails = append(tails, "Traits: "+strings.Join(traitNames[:n], ", "))
			}
			if len(goalNames) > 0 {
				n := len(goalNames)
				if n > 6 {
					n = 6
				}
				tails = append(tails, "Active goals: "+strings.Join(goalNames[:n], ", "))
			}
			if len(tails) > 0 {
				tail := " "
				for _, p := range tails {
					tail += p + ". "
				}
				tail = strings.TrimSuffix(tail, " ")
				if utf8.RuneCountInString(summary)+utf8.RuneCountInString(tail) <= maxChars+240 {
					if summary == "" {
						summary = display + "."
					}
					summary += tail
				}
			}
			var embedding []float64
			if summary != "" {
				if v, eerr := embedder.Create(ctx, summary); eerr == nil {
					embedding = v
				}
			}
			mentionCount := entity.MentionCount + 1
			salience := entity.Salience + 1
			params := map[string]any{"uuid": uuid, "profile_summary": summary, "mention_count": mentionCount, "salience": salience, "last_seen_at": utcNow()}
			var q string
			if len(embedding) > 0 {
				params["profile_embedding"] = embedding
				q = `UPDATE type::record("entity", $uuid) SET
 profile_summary=$profile_summary, profile_embedding=$profile_embedding,
 mention_count=$mention_count, salience=$salience, last_seen_at=$last_seen_at;`
			} else {
				q = `UPDATE type::record("entity", $uuid) SET
 profile_summary=$profile_summary, mention_count=$mention_count,
 salience=$salience, last_seen_at=$last_seen_at;`
			}
			if _, err := driver.Query(ctx, q, params); err != nil {
				return
			}
			refreshed++
		}()
	}
	return refreshed
}

func BackfillProfiles(ctx context.Context, driver Queryer, embedder Embedder, llm LLMClient, groupID string, batchSize int) (int, error) {
	if batchSize == 0 {
		batchSize = 50
	}
	raw, err := driver.Query(ctx, "SELECT uuid FROM entity WHERE group_id = $g;", map[string]any{"g": groupID})
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for _, row := range UnwrapRows(raw) {
		if id := stringFromAny(row["uuid"]); id != "" {
			ids = append(ids, id)
		}
	}
	total := 0
	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		total += RefreshEntityProfiles(ctx, driver, embedder, llm, groupID, ids[i:end], 30, 800)
	}
	return total, nil
}
