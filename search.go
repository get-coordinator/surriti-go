package surriti

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	DefaultSearchLimit = 10
	RRFK               = 60
)

type Reranker string

const (
	RerankRRF             Reranker = "rrf"
	RerankMMR             Reranker = "mmr"
	RerankCrossEncoder    Reranker = "cross_encoder"
	RerankNodeDistance    Reranker = "node_distance"
	RerankEpisodeMentions Reranker = "episode_mentions"
)

type SearchConfig struct {
	Limit                       int
	CandidateLimit              int
	UseVector                   bool
	UseFulltext                 bool
	OnlyValid                   bool
	FocalUUID                   *string
	Reranker                    Reranker
	MMRLambda                   float64
	CrossEncoder                CrossEncoder
	IncludeNodes                bool
	IncludeEpisodes             bool
	IncludeCommunities          bool
	Filters                     *SearchFilters
	DecayAware                  bool
	DecayHalfLifeOverrides      map[string]float64
	IncludeZeroVitality         bool
	AdmitCosine                 float64
	AdmitLexicalTokens          int
	ResurrectCosine             float64
	SpreadingActivationWeight   float64
	EvidenceSentencesPerEdge    int
}

func DefaultSearchConfig() SearchConfig {
	return SearchConfig{
		Limit: DefaultSearchLimit,
		CandidateLimit: 50,
		UseVector: true,
		UseFulltext: true,
		OnlyValid: true,
		Reranker: RerankRRF,
		MMRLambda: 0.5,
		AdmitCosine: 0.25,
		AdmitLexicalTokens: 2,
		ResurrectCosine: 0.45,
		SpreadingActivationWeight: 0.15,
		EvidenceSentencesPerEdge: 2,
	}
}

func normalizeSearchConfig(cfg *SearchConfig) SearchConfig {
	if cfg == nil {
		return DefaultSearchConfig()
	}
	out := *cfg
	if out.Limit == 0 { out.Limit = DefaultSearchLimit }
	if out.CandidateLimit == 0 { out.CandidateLimit = 50 }
	if out.Reranker == "" { out.Reranker = RerankRRF }
	if out.MMRLambda == 0 { out.MMRLambda = 0.5 }
	if out.AdmitCosine == 0 { out.AdmitCosine = 0.25 }
	if out.AdmitLexicalTokens == 0 { out.AdmitLexicalTokens = 2 }
	if out.ResurrectCosine == 0 { out.ResurrectCosine = 0.45 }
	if out.SpreadingActivationWeight == 0 { out.SpreadingActivationWeight = 0.15 }
	if out.EvidenceSentencesPerEdge == 0 { out.EvidenceSentencesPerEdge = 2 }
	// A literal zero-value Go config should behave like Python SearchConfig().
	zero := SearchConfig{}
	if *cfg == zero {
		return DefaultSearchConfig()
	}
	return out
}

type SearchResults struct {
	Edges       []EntityEdge
	Nodes       []EntityNode
	Episodes    []EpisodicNode
	Communities []CommunityNode
	Scores      map[string]float64
}

func VectorSearchEdges(ctx context.Context, driver Queryer, queryEmbedding []float64, groupID *string, limit int, onlyValid bool, allowedEdgeUUIDs []string) ([]map[string]any, error) {
	where := "WHERE fact_embedding IS NOT NONE"
	params := map[string]any{"vec": queryEmbedding, "allowed_edge_uuids": allowedEdgeUUIDs}
	if groupID != nil {
		where += " AND group_id = $group_id"
		params["group_id"] = *groupID
	}
	if allowedEdgeUUIDs != nil {
		where += " AND uuid IN $allowed_edge_uuids"
	}
	if onlyValid {
		where += ` AND status = "active" AND (invalid_at IS NONE OR invalid_at > time::now()) AND (expired_at IS NONE OR expired_at > time::now())`
	}
	q := fmt.Sprintf("SELECT * FROM relates_to\n%s\n AND fact_embedding <|%d,40|> $vec\nLIMIT %d;", where, limit, limit)
	rows, err := driver.Query(ctx, q, params)
	if err != nil { return nil, err }
	return UnwrapRows(rows), nil
}

func FulltextSearchEdges(ctx context.Context, driver Queryer, query string, groupID *string, limit int, onlyValid bool, allowedEdgeUUIDs []string) ([]map[string]any, error) {
	where := "WHERE fact @0@ $q"
	params := map[string]any{"q": query, "allowed_edge_uuids": allowedEdgeUUIDs}
	if groupID != nil {
		where += " AND group_id = $group_id"
		params["group_id"] = *groupID
	}
	if allowedEdgeUUIDs != nil {
		where += " AND uuid IN $allowed_edge_uuids"
	}
	if onlyValid {
		where += ` AND status = "active" AND (invalid_at IS NONE OR invalid_at > time::now()) AND (expired_at IS NONE OR expired_at > time::now())`
	}
	q := fmt.Sprintf("SELECT *, search::score(1) AS score FROM relates_to %s ORDER BY score DESC LIMIT %d;", where, limit)
	rows, err := driver.Query(ctx, q, params)
	if err != nil { return nil, err }
	return UnwrapRows(rows), nil
}

func filterValid(rows []map[string]any, now time.Time) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if v := coerceTime(row["invalid_at"]); v != nil && !v.After(now) { continue }
		if v := coerceTime(row["expired_at"]); v != nil && !v.After(now) { continue }
		if status := stringFromAny(row["status"]); status != "" && status != "active" { continue }
		out = append(out, row)
	}
	return out
}

func HybridSearch(ctx context.Context, driver Queryer, query string, queryEmbedding []float64, groupID *string, config *SearchConfig, egoFilter []string, allowedEdgeUUIDs []string) (SearchResults, error) {
	cfg := normalizeSearchConfig(config)
	rankings := [][]string{}
	rawByUUID := map[string]map[string]any{}

	if cfg.UseVector && queryEmbedding != nil {
		hits, err := VectorSearchEdges(ctx, driver, queryEmbedding, groupID, cfg.CandidateLimit, cfg.OnlyValid, allowedEdgeUUIDs)
		if err != nil { return SearchResults{}, err }
		rank := []string{}
		for _, hit := range hits {
			uid := stringFromAny(hit["uuid"])
			if uid == "" { continue }
			if _, ok := rawByUUID[uid]; !ok { rawByUUID[uid] = hit }
			rank = append(rank, uid)
		}
		rankings = append(rankings, rank)
	}
	if cfg.UseFulltext && strings.TrimSpace(query) != "" {
		hits, err := FulltextSearchEdges(ctx, driver, query, groupID, cfg.CandidateLimit, cfg.OnlyValid, allowedEdgeUUIDs)
		if err != nil { return SearchResults{}, err }
		rank := []string{}
		for _, hit := range hits {
			uid := stringFromAny(hit["uuid"])
			if uid == "" { continue }
			if _, ok := rawByUUID[uid]; !ok { rawByUUID[uid] = hit }
			rank = append(rank, uid)
		}
		rankings = append(rankings, rank)
	}

	fused := RRF(rankings, RRFK)
	candidates := make([]map[string]any, 0, len(fused))
	for uid := range fused {
		if row := rawByUUID[uid]; row != nil { candidates = append(candidates, row) }
	}
	if cfg.OnlyValid { candidates = filterValid(candidates, utcNow()) }
	filtered := candidates[:0]
	for _, c := range candidates {
		if EdgePassesFilters(c, cfg.Filters) { filtered = append(filtered, c) }
	}
	candidates = filtered

	if len(egoFilter) > 0 {
		ego := map[string]struct{}{}
		for _, id := range egoFilter { ego[id] = struct{}{} }
		filtered = make([]map[string]any, 0, len(candidates))
		for _, c := range candidates {
			_, src := ego[stripRecordID(c["in"])]
			_, dst := ego[stripRecordID(c["out"])]
			if src || dst { filtered = append(filtered, c) }
		}
		candidates = filtered
	}

	if cfg.DecayAware {
		candidates = AdmitCandidates(candidates, query, queryEmbedding, cfg.AdmitCosine, cfg.AdmitLexicalTokens)
		if len(candidates) == 0 {
			row := ResurrectSilentMemory(ctx, driver, queryEmbedding, groupID, cfg.ResurrectCosine, cfg.Filters, egoFilter)
			if row != nil {
				uid := stringFromAny(row["uuid"])
				if uid != "" {
					min := 1.0 / float64(RRFK+1)
					if fused[uid] < min { fused[uid] = min }
				}
				candidates = []map[string]any{row}
			}
		}
	}

	var err error
	candidates, err = applyReranker(ctx, driver, candidates, fused, cfg, query, queryEmbedding)
	if err != nil { return SearchResults{}, err }

	if cfg.DecayAware && len(candidates) > 0 {
		now := utcNow()
		kept := make([]map[string]any, 0, len(candidates))
		for _, c := range candidates {
			uid := stringFromAny(c["uuid"])
			if uid == "" { continue }
			eff := EffectiveConfidence(ParseEdge(c), now, cfg.DecayHalfLifeOverrides)
			if eff <= 0 && !cfg.IncludeZeroVitality {
				fused[uid] = 0
				continue
			}
			fused[uid] *= eff
			kept = append(kept, c)
		}
		candidates = kept
		ApplySpreadingActivation(candidates, fused, cfg.SpreadingActivationWeight, 4)
		sort.SliceStable(candidates, func(i, j int) bool {
			return fused[stringFromAny(candidates[i]["uuid"])] > fused[stringFromAny(candidates[j]["uuid"])]
		})
	}

	limit := cfg.Limit
	if limit > len(candidates) { limit = len(candidates) }
	if limit < 0 { limit = 0 }
	selected := candidates[:limit]
	if cfg.DecayAware && len(selected) > 0 && strings.TrimSpace(query) != "" {
		AttachEpisodeEvidence(ctx, driver, selected, query, groupID, cfg.EvidenceSentencesPerEdge, 6)
	}
	edges := make([]EntityEdge, 0, len(selected))
	for _, row := range selected { edges = append(edges, ParseEdge(row)) }
	return SearchResults{Edges: edges, Nodes: []EntityNode{}, Episodes: []EpisodicNode{}, Communities: []CommunityNode{}, Scores: fused}, nil
}

func applyReranker(ctx context.Context, driver Queryer, candidates []map[string]any, fused map[string]float64, cfg SearchConfig, query string, queryEmbedding []float64) ([]map[string]any, error) {
	if cfg.FocalUUID != nil || cfg.Reranker == RerankNodeDistance {
		if cfg.FocalUUID == nil {
			return nil, fmt.Errorf("Reranker.node_distance requires SearchConfig.focal_uuid")
		}
		return rerankByFocalDistance(ctx, driver, candidates, *cfg.FocalUUID, fused)
	}
	switch cfg.Reranker {
	case RerankMMR:
		return MMRRerank(candidates, queryEmbedding, "fact_embedding", cfg.MMRLambda, cfg.Limit), nil
	case RerankCrossEncoder:
		if cfg.CrossEncoder == nil { return nil, fmt.Errorf("Reranker.cross_encoder requires SearchConfig.cross_encoder") }
		return CrossEncoderRerank(ctx, candidates, query, "fact", cfg.CrossEncoder, cfg.Limit)
	case RerankEpisodeMentions:
		return EpisodeMentionsRerank(candidates, cfg.Limit), nil
	default:
		out := append([]map[string]any(nil), candidates...)
		sort.SliceStable(out, func(i, j int) bool { return fused[stringFromAny(out[i]["uuid"])] > fused[stringFromAny(out[j]["uuid"])] })
		return out, nil
	}
}

func rerankByFocalDistance(ctx context.Context, driver Queryer, candidates []map[string]any, focalUUID string, fused map[string]float64) ([]map[string]any, error) {
	q := `
LET $focal = (SELECT * FROM entity WHERE uuid = $focal_uuid LIMIT 1)[0];
RETURN IF $focal == NONE THEN [] ELSE
    array::concat(
        (SELECT uuid, 1 AS depth FROM $focal->relates_to),
        (SELECT uuid, 1 AS depth FROM $focal<-relates_to),
        (SELECT uuid, 2 AS depth FROM $focal->relates_to->entity->relates_to),
        (SELECT uuid, 2 AS depth FROM $focal->relates_to->entity<-relates_to)
    )
END;`
	result, err := driver.Query(ctx, q, map[string]any{"focal_uuid": focalUUID})
	if err != nil { return nil, err }
	distances := map[string]int{}
	for _, row := range UnwrapRows(result) {
		uid := stringFromAny(row["uuid"])
		depth := intFromAny(row["depth"])
		if depth == 0 { depth = 99 }
		if old, ok := distances[uid]; uid != "" && (!ok || depth < old) { distances[uid] = depth }
	}
	out := append([]map[string]any(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		ui, uj := stringFromAny(out[i]["uuid"]), stringFromAny(out[j]["uuid"])
		di, ok := distances[ui]; if !ok { di = 99 }
		dj, ok := distances[uj]; if !ok { dj = 99 }
		if di != dj { return di < dj }
		si := fused[ui]
		if si == 0 { if v, ok := toFloat(out[i]["_score"]); ok { si = v } }
		sj := fused[uj]
		if sj == 0 { if v, ok := toFloat(out[j]["_score"]); ok { sj = v } }
		return si > sj
	})
	return out, nil
}

func VectorSearchNodes(ctx context.Context, driver Queryer, queryEmbedding []float64, groupID *string, limit int) ([]map[string]any, error) {
	where := "WHERE name_embedding IS NOT NONE"
	params := map[string]any{"vec": queryEmbedding}
	if groupID != nil { where += " AND group_id = $group_id"; params["group_id"] = *groupID }
	q := fmt.Sprintf("SELECT * FROM entity %s AND name_embedding <|%d,40|> $vec LIMIT %d;", where, limit, limit)
	rows, err := driver.Query(ctx, q, params)
	if err != nil { return nil, err }
	return UnwrapRows(rows), nil
}

func FulltextSearchNodes(ctx context.Context, driver Queryer, query string, groupID *string, limit int) ([]map[string]any, error) {
	where := "WHERE name @0@ $q"
	params := map[string]any{"q": query}
	if groupID != nil { where += " AND group_id = $group_id"; params["group_id"] = *groupID }
	rows, err := driver.Query(ctx, fmt.Sprintf("SELECT * FROM entity %s LIMIT %d;", where, limit), params)
	if err != nil { return nil, err }
	return UnwrapRows(rows), nil
}

func FulltextSearchEpisodes(ctx context.Context, driver Queryer, query string, groupID *string, limit int) ([]map[string]any, error) {
	where := "WHERE content @0@ $q"
	params := map[string]any{"q": query}
	if groupID != nil { where += " AND group_id = $group_id"; params["group_id"] = *groupID }
	rows, err := driver.Query(ctx, fmt.Sprintf("SELECT * FROM episode %s LIMIT %d;", where, limit), params)
	if err != nil { return nil, err }
	return UnwrapRows(rows), nil
}

func VectorSearchCommunities(ctx context.Context, driver Queryer, queryEmbedding []float64, groupID *string, limit int) ([]map[string]any, error) {
	where := "WHERE name_embedding IS NOT NONE"
	params := map[string]any{"vec": queryEmbedding}
	if groupID != nil { where += " AND group_id = $group_id"; params["group_id"] = *groupID }
	q := fmt.Sprintf("SELECT * FROM community %s AND name_embedding <|%d,40|> $vec LIMIT %d;", where, limit, limit)
	rows, err := driver.Query(ctx, q, params)
	if err != nil { return nil, err }
	return UnwrapRows(rows), nil
}

func SearchNodes(ctx context.Context, driver Queryer, query string, queryEmbedding []float64, groupID *string, limit int, filters *SearchFilters) ([]EntityNode, error) {
	rankings := [][]string{}
	byUUID := map[string]map[string]any{}
	if queryEmbedding != nil {
		hits, err := VectorSearchNodes(ctx, driver, queryEmbedding, groupID, limit)
		if err != nil { return nil, err }
		rank := []string{}
		for _, hit := range hits {
			uid := stringFromAny(hit["uuid"]); if uid == "" { continue }
			if _, ok := byUUID[uid]; !ok { byUUID[uid] = hit }
			rank = append(rank, uid)
		}
		rankings = append(rankings, rank)
	}
	if strings.TrimSpace(query) != "" {
		hits, err := FulltextSearchNodes(ctx, driver, query, groupID, limit)
		if err != nil { return nil, err }
		rank := []string{}
		for _, hit := range hits {
			uid := stringFromAny(hit["uuid"]); if uid == "" { continue }
			if _, ok := byUUID[uid]; !ok { byUUID[uid] = hit }
			rank = append(rank, uid)
		}
		rankings = append(rankings, rank)
	}
	fused := RRF(rankings, RRFK)
	rows := make([]map[string]any, 0, len(byUUID))
	for _, row := range byUUID { if NodePassesFilters(row, filters) { rows = append(rows, row) } }
	sort.SliceStable(rows, func(i,j int) bool { return fused[stringFromAny(rows[i]["uuid"])] > fused[stringFromAny(rows[j]["uuid"])] })
	if limit > len(rows) { limit = len(rows) }
	out := make([]EntityNode, 0, limit)
	for _, row := range rows[:limit] { out = append(out, ParseEntity(row)) }
	return out, nil
}

func SearchEpisodes(ctx context.Context, driver Queryer, query string, groupID *string, limit int) ([]EpisodicNode, error) {
	if strings.TrimSpace(query) == "" { return []EpisodicNode{}, nil }
	rows, err := FulltextSearchEpisodes(ctx, driver, query, groupID, limit)
	if err != nil { return nil, err }
	out := make([]EpisodicNode, 0, len(rows))
	for _, row := range rows { out = append(out, ParseEpisode(row)) }
	return out, nil
}

func SearchCommunities(ctx context.Context, driver Queryer, query string, queryEmbedding []float64, groupID *string, limit int) ([]CommunityNode, error) {
	rows := []map[string]any{}
	if queryEmbedding != nil {
		hits, err := VectorSearchCommunities(ctx, driver, queryEmbedding, groupID, limit)
		if err != nil { return nil, err }
		rows = append(rows, hits...)
	}
	seen := map[string]map[string]any{}
	order := []string{}
	for _, row := range rows {
		uid := stringFromAny(row["uuid"])
		if uid == "" { continue }
		if _, ok := seen[uid]; !ok { seen[uid] = row; order = append(order, uid) }
	}
	if limit > len(order) { limit = len(order) }
	out := make([]CommunityNode, 0, limit)
	for _, uid := range order[:limit] { out = append(out, ParseCommunity(seen[uid])) }
	return out, nil
}
