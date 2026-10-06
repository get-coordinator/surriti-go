package surriti

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type MemoryContext struct {
	Query            string
	Profiles         []EntityNode
	Facts            []EntityEdge
	Episodes         []EpisodicNode
	Communities      []CommunityNode
	ResolvedEntities []map[string]any
	Traits           []EntityNode
	Goals            []EntityNode
	Prediction       map[string]any
	SelfModel        map[string]any
}

type RecallOptions struct {
	GroupID         string
	Depth           string
	AsOf            *time.Time
	Limit           int
	IncludeInvalid  bool
	MemoryClasses   []string
	IncludeEdges    bool
	IncludeEntities bool
}

func queryMentions(query string) []ExtractedEntity {
	words := []string{}
	for _, t := range strings.Fields(strings.ReplaceAll(query, ",", " ")) {
		if len(t) > 1 {
			words = append(words, t)
		}
	}
	out := []ExtractedEntity{}
	seen := map[string]struct{}{}
	for _, n := range []int{3, 2, 1} {
		for i := 0; i+n <= len(words); i++ {
			phrase := strings.Join(words[i:i+n], " ")
			key := casefold(phrase)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, ExtractedEntity{Name: phrase, Labels: []string{"Entity"}})
		}
	}
	return out
}

func uniqueResolvedUUIDs(resolved []ResolvedEntity) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, r := range resolved {
		if r.CanonicalUUID == nil || *r.CanonicalUUID == "" {
			continue
		}
		if _, ok := seen[*r.CanonicalUUID]; ok {
			continue
		}
		seen[*r.CanonicalUUID] = struct{}{}
		out = append(out, *r.CanonicalUUID)
	}
	return out
}

func (s *Surriti) Recall(ctx context.Context, query string, opts RecallOptions) (MemoryContext, error) {
	depth := opts.Depth
	if depth == "" {
		depth = "normal"
	}
	if depth != "fast" && depth != "normal" && depth != "deep" {
		return MemoryContext{}, fmt.Errorf("%w: depth must be one of 'fast', 'normal', 'deep'", ErrConfig)
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 20
	}
	mentions := queryMentions(query)
	resolved := []ResolvedEntity{}
	var err error
	if len(mentions) > 0 {
		resolved, err = ResolveEntityMentions(ctx, s.Driver, s.Embedder, s.LLM, mentions, opts.GroupID, query, s.AliasResolutionThreshold, false, false, nil, nil)
		if err != nil {
			return MemoryContext{}, err
		}
	}
	ego := uniqueResolvedUUIDs(resolved)

	profiles := []EntityNode{}
	if len(ego) > 0 {
		raw, err := s.Driver.Query(ctx, "SELECT * FROM entity WHERE group_id = $g AND uuid IN $u;", map[string]any{"g": opts.GroupID, "u": ego})
		if err != nil {
			return MemoryContext{}, err
		}
		for _, row := range UnwrapRows(raw) {
			profiles = append(profiles, ParseEntity(row))
		}
	}
	var embedding []float64
	if query != "" {
		embedding, err = s.Embedder.Create(ctx, query)
		if err != nil {
			return MemoryContext{}, err
		}
	}
	facts := []EntityEdge{}
	if opts.AsOf != nil {
		for _, subject := range ego {
			state, err := s.GetStateAsOf(ctx, subject, opts.AsOf.UTC(), opts.GroupID, nil, nil)
			if err != nil {
				return MemoryContext{}, err
			}
			for _, e := range state {
				facts = append(facts, e)
			}
		}
		sort.SliceStable(facts, func(i, j int) bool {
			var a, b time.Time
			if facts[i].ValidAt != nil {
				a = *facts[i].ValidAt
			}
			if facts[j].ValidAt != nil {
				b = *facts[j].ValidAt
			}
			return a.After(b)
		})
		seen := map[string]struct{}{}
		dedup := facts[:0]
		for _, e := range facts {
			if e.UUID == "" {
				continue
			}
			if _, ok := seen[e.UUID]; ok {
				continue
			}
			seen[e.UUID] = struct{}{}
			dedup = append(dedup, e)
		}
		facts = dedup
		if len(facts) > limit {
			facts = facts[:limit]
		}
	} else {
		var filters *SearchFilters
		if len(opts.MemoryClasses) > 0 {
			filters = &SearchFilters{EdgeMemoryClasses: append([]string(nil), opts.MemoryClasses...)}
		}
		cfg := DefaultSearchConfig()
		cfg.Limit = limit
		cfg.CandidateLimit = limit * 4
		if cfg.CandidateLimit < 40 {
			cfg.CandidateLimit = 40
		}
		cfg.OnlyValid = !opts.IncludeInvalid
		cfg.Filters = filters
		cfg.DecayAware = s.CognitionConfig.Enabled && s.CognitionConfig.DecayAwareRecall
		cfg.DecayHalfLifeOverrides = s.CognitionConfig.DecayHalfLifeDays
		results, err := HybridSearch(ctx, s.Driver, query, embedding, &opts.GroupID, &cfg, ego, nil)
		if err != nil {
			return MemoryContext{}, err
		}
		facts = results.Edges
		if s.CognitionConfig.Enabled && len(facts) > 0 {
			ids := make([]string, 0, len(facts))
			for _, f := range facts {
				if f.UUID != "" {
					ids = append(ids, f.UUID)
				}
			}
			_, _ = ReinforceEdgesOnRecall(ctx, s.Driver, opts.GroupID, ids, 1)
		}
		if len(opts.MemoryClasses) > 0 && len(facts) == 0 {
			classes := []string{}
			for _, v := range opts.MemoryClasses {
				if v = strings.ToLower(strings.TrimSpace(v)); v != "" {
					classes = append(classes, v)
				}
			}
			raw, qerr := s.Driver.Query(ctx, `
SELECT * FROM relates_to
WHERE group_id = $g
 AND status = "active"
 AND invalid_at IS NONE
 AND (attributes.memory_class IN $classes
      OR (attributes.memory_class IS NONE AND "objective" IN $classes))
ORDER BY created_at DESC
LIMIT $lim;`, map[string]any{"g": opts.GroupID, "classes": classes, "lim": limit})
			if qerr != nil {
				return MemoryContext{}, qerr
			}
			for _, row := range UnwrapRows(raw) {
				facts = append(facts, ParseEdge(row))
			}
			if s.CognitionConfig.Enabled && len(facts) > 0 {
				ids := make([]string, 0, len(facts))
				for _, f := range facts {
					ids = append(ids, f.UUID)
				}
				_, _ = ReinforceEdgesOnRecall(ctx, s.Driver, opts.GroupID, ids, 1)
			}
		}
	}

	episodes := []EpisodicNode{}
	communities := []CommunityNode{}
	var prediction map[string]any
	if depth == "deep" {
		episodes, err = SearchEpisodes(ctx, s.Driver, query, &opts.GroupID, limit)
		if err != nil {
			return MemoryContext{}, err
		}
		communities, err = SearchCommunities(ctx, s.Driver, query, embedding, &opts.GroupID, limit)
		if err != nil {
			return MemoryContext{}, err
		}
		if raw, qerr := s.Driver.Query(ctx, "SELECT payload FROM community WHERE group_id = $g AND kind = 'prediction' LIMIT 1;", map[string]any{"g": opts.GroupID}); qerr == nil {
			rows := UnwrapRows(raw)
			if len(rows) > 0 {
				p := mapFromAny(rows[0]["payload"])
				if len(p) > 0 {
					prediction = p
				}
			}
		}
	}

	traits := []EntityNode{}
	goals := []EntityNode{}
	traitIDs := []string{}
	goalIDs := []string{}
	for _, p := range profiles {
		traitIDs = append(traitIDs, p.Traits...)
		goalIDs = append(goalIDs, p.GoalsActive...)
	}
	traitIDs = orderedUniqueStrings(traitIDs)
	goalIDs = orderedUniqueStrings(goalIDs)
	sidecars := append(append([]string(nil), traitIDs...), goalIDs...)
	if len(sidecars) > 0 {
		if raw, qerr := s.Driver.Query(ctx, "SELECT * FROM entity WHERE group_id = $g AND uuid IN $u;", map[string]any{"g": opts.GroupID, "u": sidecars}); qerr == nil {
			byID := map[string]EntityNode{}
			for _, row := range UnwrapRows(raw) {
				n := ParseEntity(row)
				byID[n.UUID] = n
			}
			for _, id := range traitIDs {
				if n, ok := byID[id]; ok {
					traits = append(traits, n)
				}
			}
			for _, id := range goalIDs {
				if n, ok := byID[id]; ok {
					goals = append(goals, n)
				}
			}
		}
	}

	var selfModel map[string]any
	if s.CognitionConfig.SelfAwareness {
		if model, qerr := s.GetSelfModel(ctx, opts.GroupID); qerr == nil {
			selfModel = model
		}
	}
	resolvedRows := []map[string]any{}
	for _, r := range resolved {
		if r.CanonicalUUID == nil {
			continue
		}
		resolvedRows = append(resolvedRows, map[string]any{"mention": r.Mention.Name, "uuid": *r.CanonicalUUID, "name": r.CanonicalName, "resolution": string(r.Resolution), "confidence": r.Confidence})
	}
	return MemoryContext{Query: query, Profiles: profiles, Facts: facts, Episodes: episodes, Communities: communities, ResolvedEntities: resolvedRows, Traits: traits, Goals: goals, Prediction: prediction, SelfModel: selfModel}, nil
}

func orderedUniqueStrings(values []string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
