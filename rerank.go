package surriti

import (
	"context"
	"sort"
	"strings"
)

type CrossEncoder interface {
	Rank(context.Context, string, []string) ([]RankedPassage, error)
}

type RankedPassage struct {
	Passage string
	Score   float64
}

type DummyCrossEncoder struct{}

func (DummyCrossEncoder) Rank(_ context.Context, query string, passages []string) ([]RankedPassage, error) {
	q := map[string]struct{}{}
	for _, t := range strings.Fields(query) {
		if t != "" {
			q[strings.ToLower(t)] = struct{}{}
		}
	}
	denom := len(q)
	if denom < 1 {
		denom = 1
	}
	out := make([]RankedPassage, 0, len(passages))
	for _, p := range passages {
		pt := map[string]struct{}{}
		for _, t := range strings.Fields(p) {
			if t != "" {
				pt[strings.ToLower(t)] = struct{}{}
			}
		}
		hits := 0
		for t := range q {
			if _, ok := pt[t]; ok {
				hits++
			}
		}
		out = append(out, RankedPassage{Passage: p, Score: float64(hits) / float64(denom)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func RRF(rankings [][]string, k int) map[string]float64 {
	if k == 0 {
		k = 60
	}
	scores := map[string]float64{}
	for _, ranked := range rankings {
		for rank, uuid := range ranked {
			scores[uuid] += 1.0 / float64(k+rank+1)
		}
	}
	return scores
}

func MMRRerank(candidates []map[string]any, queryEmbedding []float64, embeddingField string, lambdaMult float64, limit int) []map[string]any {
	if limit < 0 {
		limit = 0
	}
	if queryEmbedding == nil || len(candidates) == 0 {
		if limit > len(candidates) {
			limit = len(candidates)
		}
		return append([]map[string]any(nil), candidates[:limit]...)
	}
	pool := append([]map[string]any(nil), candidates...)
	selected := make([]map[string]any, 0, minInt(limit, len(pool)))
	for len(pool) > 0 && len(selected) < limit {
		bestIdx := 0
		bestScore := -1e9
		for i, cand := range pool {
			emb := rowVector(cand[embeddingField])
			if emb == nil {
				continue
			}
			relevance := CosineSimilarity(queryEmbedding, emb)
			redundancy := 0.0
			for _, s := range selected {
				if semb := rowVector(s[embeddingField]); semb != nil {
					if sim := CosineSimilarity(emb, semb); sim > redundancy {
						redundancy = sim
					}
				}
			}
			score := lambdaMult*relevance - (1-lambdaMult)*redundancy
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		selected = append(selected, pool[bestIdx])
		pool = append(pool[:bestIdx], pool[bestIdx+1:]...)
	}
	return selected
}

func CrossEncoderRerank(ctx context.Context, candidates []map[string]any, query, textField string, crossEncoder CrossEncoder, limit int) ([]map[string]any, error) {
	if len(candidates) == 0 {
		return []map[string]any{}, nil
	}
	passages := make([]string, len(candidates))
	for i, c := range candidates {
		passages[i] = asString(c[textField])
	}
	ranked, err := crossEncoder.Rank(ctx, query, passages)
	if err != nil {
		return nil, err
	}
	order := map[string]int{}
	for i, r := range ranked {
		if _, exists := order[r.Passage]; !exists {
			order[r.Passage] = i
		}
	}
	out := append([]map[string]any(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		oi, iok := order[asString(out[i][textField])]
		oj, jok := order[asString(out[j][textField])]
		if !iok {
			oi = len(out)
		}
		if !jok {
			oj = len(out)
		}
		return oi < oj
	})
	if limit < 0 {
		limit = 0
	}
	if limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

func EpisodeMentionsRerank(candidates []map[string]any, limit int) []map[string]any {
	out := append([]map[string]any(nil), candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		return len(asStringSlice(out[i]["episodes"])) > len(asStringSlice(out[j]["episodes"]))
	})
	if limit < 0 {
		limit = 0
	}
	if limit < len(out) {
		out = out[:limit]
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
