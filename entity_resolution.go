package surriti

import (
	"context"
	"regexp"
	"sort"
	"strings"
)

var aliasPunctRE = regexp.MustCompile(`[^\p{L}\p{N}_\s'-]`)

func NormalizeAlias(name string) string {
	if name == "" {
		return ""
	}
	cleaned := aliasPunctRE.ReplaceAllString(name, " ")
	cleaned = strings.ToLower(cleaned)
	return strings.Join(strings.Fields(cleaned), " ")
}

type ResolutionKind string

const (
	ResolutionAliasHit      ResolutionKind = "alias_hit"
	ResolutionExactName     ResolutionKind = "exact_name"
	ResolutionSemanticMatch ResolutionKind = "semantic_match"
	ResolutionLLMMatch      ResolutionKind = "llm_match"
	ResolutionNew           ResolutionKind = "new"
)

type ResolvedEntity struct {
	Mention       ExtractedEntity
	CanonicalUUID *string
	CanonicalName string
	Resolution    ResolutionKind
	Confidence    float64
	Existing      *EntityNode
}

type AliasCandidate struct {
	UUID    string
	Name    string
	Summary string
}

type EntityAliasResolutionRequest struct {
	Mention        ExtractedEntity
	Candidates     []AliasCandidate
	EpisodeContext string
}

type EntityAliasResolver interface {
	ResolveEntityAlias(context.Context, EntityAliasResolutionRequest) (*string, error)
}

type LLMResolverFunc func(context.Context, ExtractedEntity, []AliasCandidate, string) (*string, error)

func defaultLLMResolver(ctx context.Context, llm LLMClient, mention ExtractedEntity, candidates []AliasCandidate, episodeContext string) (*string, error) {
	if llm == nil || len(candidates) == 0 {
		return nil, nil
	}
	resolver, ok := llm.(EntityAliasResolver)
	if !ok {
		return nil, nil
	}
	return resolver.ResolveEntityAlias(ctx, EntityAliasResolutionRequest{
		Mention: mention, Candidates: candidates, EpisodeContext: episodeContext,
	})
}

func ResolveEntityMentions(
	ctx context.Context,
	driver Queryer,
	embedder Embedder,
	llm LLMClient,
	mentions []ExtractedEntity,
	groupID string,
	episodeContext string,
	threshold float64,
	useLLM bool,
	createMissing bool,
	episodeUUID *string,
	llmResolver LLMResolverFunc,
) ([]ResolvedEntity, error) {
	if len(mentions) == 0 {
		return []ResolvedEntity{}, nil
	}
	if threshold == 0 {
		threshold = 0.86
	}

	normKeys := make([]string, len(mentions))
	uniqueNorm := []string{}
	seenNorm := map[string]struct{}{}
	for i, m := range mentions {
		k := NormalizeAlias(m.Name)
		normKeys[i] = k
		if k != "" {
			if _, ok := seenNorm[k]; !ok {
				seenNorm[k] = struct{}{}
				uniqueNorm = append(uniqueNorm, k)
			}
		}
	}

	aliasToEntity := map[string]map[string]any{}
	if len(uniqueNorm) > 0 {
		raw, err := driver.Query(ctx,
			"SELECT * FROM entity_alias WHERE group_id = $g AND normalized_alias IN $aliases;",
			map[string]any{"g": groupID, "aliases": uniqueNorm},
		)
		if err != nil { return nil, err }
		for _, row := range UnwrapRows(raw) {
			key := stringFromAny(row["normalized_alias"])
			if _, ok := aliasToEntity[key]; !ok {
				aliasToEntity[key] = row
			}
		}
	}

	entityRowsByUUID := map[string]EntityNode{}
	entityByNormName := map[string]EntityNode{}
	entityOrder := []EntityNode{}
	if len(uniqueNorm) > 0 || len(aliasToEntity) > 0 {
		raw, err := driver.Query(ctx, "SELECT * FROM entity WHERE group_id = $g;", map[string]any{"g": groupID})
		if err != nil { return nil, err }
		for _, row := range UnwrapRows(raw) {
			node := ParseEntity(row)
			if node.UUID != "" {
				entityRowsByUUID[node.UUID] = node
				entityOrder = append(entityOrder, node)
			}
			key := NormalizeAlias(node.Name)
			if key != "" {
				if _, ok := entityByNormName[key]; !ok {
					entityByNormName[key] = node
				}
			}
		}
	}

	results := make([]*ResolvedEntity, len(mentions))
	unresolved := []int{}
	for i, mention := range mentions {
		key := normKeys[i]
		if key == "" {
			r := ResolvedEntity{Mention: mention, CanonicalName: mention.Name, Resolution: ResolutionNew}
			results[i] = &r
			continue
		}
		if aliasRow := aliasToEntity[key]; aliasRow != nil {
			uuid := stringFromAny(aliasRow["entity_uuid"])
			if existing, ok := entityRowsByUUID[uuid]; ok {
				name := existing.Name
				if existing.CanonicalName != nil { name = *existing.CanonicalName }
				conf := 1.0
				if aliasRow["confidence"] != nil {
					if v, ok := toFloat(aliasRow["confidence"]); ok { conf = v }
				}
				u := existing.UUID
				ex := existing
				r := ResolvedEntity{Mention: mention, CanonicalUUID: &u, CanonicalName: name, Resolution: ResolutionAliasHit, Confidence: conf, Existing: &ex}
				results[i] = &r
				continue
			}
		}
		if existing, ok := entityByNormName[key]; ok {
			name := existing.Name
			if existing.CanonicalName != nil { name = *existing.CanonicalName }
			u := existing.UUID
			ex := existing
			r := ResolvedEntity{Mention: mention, CanonicalUUID: &u, CanonicalName: name, Resolution: ResolutionExactName, Confidence: 1, Existing: &ex}
			results[i] = &r
			continue
		}
		unresolved = append(unresolved, i)
	}

	ambiguous := map[int][]scoredEntity{}
	if len(unresolved) > 0 && len(entityRowsByUUID) > 0 {
		names := make([]string, len(unresolved))
		for i, idx := range unresolved { names[i] = mentions[idx].Name }
		vectors, err := CreateBatch(ctx, embedder, names)
		if err == nil {
			still := []int{}
			for local, vec := range vectors {
				if local >= len(unresolved) { break }
				idx := unresolved[local]
				scored := []scoredEntity{}
				for _, node := range entityOrder {
					if len(node.NameEmbedding) == 0 { continue }
					score := CosineSimilarity(vec, node.NameEmbedding)
					if score >= threshold {
						scored = append(scored, scoredEntity{node: node, score: score})
					}
				}
				sort.SliceStable(scored, func(i,j int) bool { return scored[i].score > scored[j].score })
				if len(scored) == 0 {
					still = append(still, idx)
					continue
				}
				top := scored[0]
				if len(scored) == 1 || top.score-scored[1].score >= .05 {
					name := top.node.Name
					if top.node.CanonicalName != nil { name = *top.node.CanonicalName }
					u := top.node.UUID
					ex := top.node
					r := ResolvedEntity{Mention: mentions[idx], CanonicalUUID: &u, CanonicalName: name, Resolution: ResolutionSemanticMatch, Confidence: top.score, Existing: &ex}
					results[idx] = &r
				} else {
					if len(scored) > 3 { scored = scored[:3] }
					ambiguous[idx] = scored
				}
			}
			if len(vectors) < len(unresolved) {
				still = append(still, unresolved[len(vectors):]...)
			}
			unresolved = append(still, sortedIntKeys(ambiguous)...)
		}

		if useLLM && len(ambiguous) > 0 {
			for _, idx := range sortedIntKeys(ambiguous) {
				scored := ambiguous[idx]
				payload := make([]AliasCandidate, 0, len(scored))
				for _, s := range scored {
					name := s.node.Name
					if s.node.CanonicalName != nil { name = *s.node.CanonicalName }
					summary := s.node.ProfileSummary
					if summary == "" { summary = s.node.Summary }
					payload = append(payload, AliasCandidate{UUID:s.node.UUID, Name:name, Summary:summary})
				}
				var winner *string
				var err error
				if llmResolver != nil {
					winner, err = llmResolver(ctx, mentions[idx], payload, episodeContext)
				} else {
					winner, err = defaultLLMResolver(ctx, llm, mentions[idx], payload, episodeContext)
				}
				// Python treats resolver failure as no-match.
				if err != nil || winner == nil { continue }
				for _, s := range scored {
					if s.node.UUID != *winner { continue }
					name := s.node.Name
					if s.node.CanonicalName != nil { name = *s.node.CanonicalName }
					u := s.node.UUID
					ex := s.node
					r := ResolvedEntity{Mention: mentions[idx], CanonicalUUID:&u, CanonicalName:name, Resolution:ResolutionLLMMatch, Confidence:.9, Existing:&ex}
					results[idx] = &r
					unresolved = removeInt(unresolved, idx)
					break
				}
			}
		}
	}

	for _, idx := range unresolved {
		if results[idx] == nil {
			r := ResolvedEntity{Mention: mentions[idx], CanonicalName: mentions[idx].Name, Resolution: ResolutionNew}
			results[idx] = &r
		}
	}

	final := make([]ResolvedEntity, 0, len(results))
	for _, r := range results {
		if r != nil { final = append(final, *r) }
	}
	if createMissing {
		recordAliases(ctx, driver, final, groupID, episodeUUID)
	}
	return final, nil
}

type scoredEntity struct { node EntityNode; score float64 }

func sortedIntKeys(m map[int][]scoredEntity) []int {
	out := make([]int,0,len(m))
	for k := range m { out=append(out,k) }
	sort.Ints(out)
	return out
}

func removeInt(xs []int, target int) []int {
	out := xs[:0]
	for _, x := range xs { if x != target { out=append(out,x) } }
	return out
}

func recordAliases(ctx context.Context, driver Queryer, resolved []ResolvedEntity, groupID string, episodeUUID *string) {
	for _, r := range resolved {
		if r.CanonicalUUID == nil || r.Existing == nil { continue }
		if r.Resolution == ResolutionAliasHit || r.Resolution == ResolutionExactName { continue }
		norm := NormalizeAlias(r.Mention.Name)
		if norm == "" { continue }
		uuid := newUUID()
		_, err := driver.Query(ctx, `
CREATE type::record("entity_alias", $uuid) CONTENT {
    uuid: $uuid,
    group_id: $group_id,
    alias: $alias,
    normalized_alias: $normalized_alias,
    entity_uuid: $entity_uuid,
    confidence: $confidence,
    source_episode_uuid: $source_episode_uuid,
    created_at: $created_at
};`, map[string]any{
			"uuid": uuid, "group_id": groupID, "alias": r.Mention.Name,
			"normalized_alias": norm, "entity_uuid": *r.CanonicalUUID,
			"confidence": r.Confidence, "source_episode_uuid": episodeUUID, "created_at": utcNow(),
		})
		if err != nil && !strings.Contains(err.Error(), "entity_alias_unique") {
			// Best-effort by contract. Non-unique errors are intentionally
			// swallowed too; ingest must not fail because alias enrichment did.
			continue
		}
	}
}
