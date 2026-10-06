package surriti

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
)

var memoryTokenRE = regexp.MustCompile(`[A-Za-z0-9_]+(?:[.-][A-Za-z0-9_]+)*`)

var memoryStop = map[string]struct{}{}
var memoryAlwaysStop = map[string]struct{}{"hi": {}, "ok": {}, "uh": {}, "um": {}}

func init() {
	for _, s := range []string{"a", "about", "all", "am", "an", "and", "any", "are", "as", "at", "be", "been", "being", "by", "can", "cool", "could", "did", "do", "does", "for", "from", "get", "going", "good", "great", "has", "have", "he", "hello", "help", "her", "here", "hey", "him", "his", "how", "i", "if", "in", "is", "it", "its", "just", "know", "like", "make", "me", "my", "need", "nice", "no", "not", "of", "okay", "on", "or", "our", "out", "please", "really", "she", "should", "so", "sure", "tell", "than", "thank", "thanks", "that", "the", "them", "then", "there", "they", "think", "this", "to", "us", "want", "was", "we", "were", "what", "when", "where", "which", "who", "will", "with", "would", "yes", "you", "your"} {
		memoryStop[s] = struct{}{}
	}
}

func CueTokens(text string, limit int) []string {
	if limit <= 0 {
		limit = 16
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, limit)
	for _, raw := range memoryTokenRE.FindAllString(text, -1) {
		token := strings.ToLower(raw)
		shortAcronym := len(raw) <= 2 && raw == strings.ToUpper(raw) && token != "a" && token != "i"
		if _, stop := memoryAlwaysStop[token]; stop {
			continue
		}
		if _, stop := memoryStop[token]; stop && !shortAcronym {
			continue
		}
		if _, dup := seen[token]; dup {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func LexicalMatchCount(row map[string]any, queryTokens []string) int {
	if len(queryTokens) == 0 {
		return 0
	}
	haystack := strings.ToLower(strings.Join([]string{asString(row["fact"]), asString(row["name"]), asString(row["canonical_name"])}, " "))
	set := map[string]struct{}{}
	for _, t := range memoryTokenRE.FindAllString(haystack, -1) {
		set[t] = struct{}{}
	}
	n := 0
	for _, t := range queryTokens {
		if _, ok := set[t]; ok {
			n++
		}
	}
	return n
}

func rowVector(v any) []float64 {
	switch x := v.(type) {
	case []float64:
		return x
	case []float32:
		r := make([]float64, len(x))
		for i, v := range x {
			r[i] = float64(v)
		}
		return r
	case []any:
		r := make([]float64, 0, len(x))
		for _, v := range x {
			if f, ok := toFloat(v); ok {
				r = append(r, f)
			} else {
				return nil
			}
		}
		return r
	default:
		return nil
	}
}

func CandidateEvidence(row map[string]any, query string, queryEmbedding []float64) (float64, int) {
	return MemoryCosineSimilarity(rowVector(row["fact_embedding"]), queryEmbedding), LexicalMatchCount(row, CueTokens(query, 16))
}

func AdmitCandidates(candidates []map[string]any, query string, queryEmbedding []float64, minCosine float64, minLexicalTokens int) []map[string]any {
	if strings.TrimSpace(query) == "" {
		return candidates
	}
	out := make([]map[string]any, 0, len(candidates))
	for _, row := range candidates {
		cos, nlex := CandidateEvidence(row, query, queryEmbedding)
		row["_memory_cosine"] = cos
		row["_memory_lexical_matches"] = nlex
		if cos >= minCosine || nlex >= minLexicalTokens {
			out = append(out, row)
		}
	}
	return out
}

func stripRecordID(v any) string {
	s := asString(v)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, ':'); i >= 0 && i+1 < len(s) {
		return strings.Trim(s[i+1:], "⟨⟩")
	}
	return strings.Trim(s, "⟨⟩")
}

func ApplySpreadingActivation(candidates []map[string]any, fused map[string]float64, weight float64, seedCount int) {
	if len(candidates) < 2 || weight <= 0 {
		return
	}
	if seedCount < 1 {
		seedCount = 1
	}
	identities := func(row map[string]any) map[string]struct{} {
		ids := map[string]struct{}{}
		for _, key := range []string{"in", "out"} {
			if v := stripRecordID(row[key]); v != "" {
				ids["entity:"+v] = struct{}{}
			}
		}
		for _, ep := range asStringSlice(row["episodes"]) {
			if ep != "" {
				ids["episode:"+ep] = struct{}{}
			}
		}
		return ids
	}
	byID := map[string]map[string]struct{}{}
	fan := map[string]int{}
	for _, row := range candidates {
		uid := asString(row["uuid"])
		if uid == "" {
			continue
		}
		ids := identities(row)
		byID[uid] = ids
		for id := range ids {
			fan[id]++
		}
	}
	seeds := make([]map[string]any, 0, len(candidates))
	for _, r := range candidates {
		if asString(r["uuid"]) != "" {
			seeds = append(seeds, r)
		}
	}
	sort.SliceStable(seeds, func(i, j int) bool {
		return fused[asString(seeds[i]["uuid"])] > fused[asString(seeds[j]["uuid"])]
	})
	if len(seeds) > seedCount {
		seeds = seeds[:seedCount]
	}
	for _, row := range candidates {
		uid := asString(row["uuid"])
		if uid == "" {
			continue
		}
		boost := 0.0
		ids := byID[uid]
		for _, seed := range seeds {
			sid := asString(seed["uuid"])
			if sid == "" || sid == uid {
				continue
			}
			for ident := range ids {
				if _, ok := byID[sid][ident]; ok {
					damp := math.Max(0, 2-math.Log1p(float64(fan[ident])))
					boost += weight * damp
				}
			}
		}
		if boost != 0 {
			fused[uid] = fused[uid] + boost
		}
	}
}

func splitEvidenceSentences(text string) []string {
	if len(text) > 8000 {
		text = text[:8000]
	}
	var out []string
	start := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\n' {
			if s := strings.TrimSpace(text[start:i]); s != "" {
				out = append(out, s)
			}
			start = i + 1
			continue
		}
		if c == '.' || c == '!' || c == '?' {
			j := i + 1
			for j < len(text) && (text[j] == '.' || text[j] == '!' || text[j] == '?') {
				j++
			}
			if j == len(text) || text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r' {
				if s := strings.TrimSpace(text[start:j]); s != "" {
					out = append(out, s)
				}
				start = j
				i = j - 1
			}
		}
	}
	if start < len(text) {
		if s := strings.TrimSpace(text[start:]); s != "" {
			out = append(out, s)
		}
	}
	return out
}

type scoredSnippet struct {
	hits int
	text string
}

func EvidenceSnippets(text, query string, k, maxChars int) []string {
	if k <= 0 {
		k = 2
	}
	if maxChars <= 0 {
		maxChars = 240
	}
	tokens := CueTokens(query, 16)
	if len(tokens) == 0 || text == "" {
		return nil
	}
	var scored []scoredSnippet
	for _, sentence := range splitEvidenceSentences(text) {
		if len(sentence) < 12 {
			continue
		}
		stoks := map[string]struct{}{}
		for _, t := range memoryTokenRE.FindAllString(strings.ToLower(sentence), -1) {
			stoks[t] = struct{}{}
		}
		hits := 0
		for _, t := range tokens {
			if _, ok := stoks[t]; ok {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		orig := sentence
		if len(sentence) > maxChars {
			lower := strings.ToLower(sentence)
			center := -1
			for _, t := range tokens {
				if p := strings.Index(lower, t); p >= 0 && (center < 0 || p < center) {
					center = p
				}
			}
			if center < 0 {
				center = 0
			}
			start := center - maxChars/3
			if start < 0 {
				start = 0
			}
			if max := len(sentence) - maxChars; start > max {
				start = max
			}
			end := start + maxChars
			sentence = sentence[start:end]
			if start > 0 {
				sentence = "..." + sentence
			}
			if end < len(orig) {
				sentence += "..."
			}
		}
		scored = append(scored, scoredSnippet{hits, sentence})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].hits != scored[j].hits {
			return scored[i].hits > scored[j].hits
		}
		return len(scored[i].text) < len(scored[j].text)
	})
	if len(scored) > k {
		scored = scored[:k]
	}
	out := make([]string, len(scored))
	for i, s := range scored {
		out[i] = s.text
	}
	return out
}


// AttachEpisodeEvidence mirrors Python's fail-soft recall enrichment. It mutates
// the candidate attribute bags in place and deliberately suppresses storage
// errors so evidence decoration can never make recall fail.
func AttachEpisodeEvidence(ctx context.Context, driver Queryer, candidates []map[string]any, query string, groupID *string, sentencesPerEdge, maxEdges int) {
	if sentencesPerEdge <= 0 {
		sentencesPerEdge = 2
	}
	if maxEdges <= 0 {
		maxEdges = 6
	}
	if strings.TrimSpace(query) == "" {
		return
	}
	if maxEdges > len(candidates) {
		maxEdges = len(candidates)
	}
	selected := make([]map[string]any, 0, maxEdges)
	episodeIDs := []string{}
	seen := map[string]struct{}{}
	for _, row := range candidates[:maxEdges] {
		eps := asStringSlice(row["episodes"])
		if len(eps) == 0 {
			continue
		}
		selected = append(selected, row)
		for _, ep := range eps {
			if ep == "" {
				continue
			}
			if _, ok := seen[ep]; ok {
				continue
			}
			seen[ep] = struct{}{}
			episodeIDs = append(episodeIDs, ep)
		}
	}
	if len(episodeIDs) == 0 {
		return
	}
	where := "WHERE uuid IN $uuids"
	params := map[string]any{"uuids": episodeIDs}
	if groupID != nil {
		where += " AND group_id = $group_id"
		params["group_id"] = *groupID
	}
	result, err := driver.Query(ctx, "SELECT uuid, content FROM episode "+where+";", params)
	if err != nil {
		return
	}
	episodes := map[string]map[string]any{}
	for _, row := range UnwrapRows(result) {
		if id := stringFromAny(row["uuid"]); id != "" {
			episodes[id] = row
		}
	}
	for _, edge := range selected {
		evidence := []map[string]string{}
		for _, episodeID := range asStringSlice(edge["episodes"]) {
			episode := episodes[episodeID]
			if episode == nil {
				continue
			}
			for _, snippet := range EvidenceSnippets(stringFromAny(episode["content"]), query, sentencesPerEdge, 240) {
				evidence = append(evidence, map[string]string{"episode_uuid": episodeID, "text": snippet})
				if len(evidence) >= sentencesPerEdge {
					break
				}
			}
			if len(evidence) >= sentencesPerEdge {
				break
			}
		}
		if len(evidence) == 0 {
			continue
		}
		attrs := mapFromAny(edge["attributes"])
		if attrs == nil {
			attrs = map[string]any{}
		} else {
			attrs = cloneMap(attrs)
		}
		attrs["recall_evidence"] = evidence
		edge["attributes"] = attrs
	}
}

// ResurrectSilentMemory performs the Python strong-cue fallback. Storage and
// update failures intentionally collapse to nil: resurrection is opportunistic
// and must not make the base recall path fragile.
func ResurrectSilentMemory(
	ctx context.Context,
	driver Queryer,
	queryEmbedding []float64,
	groupID *string,
	minCosine float64,
	filters *SearchFilters,
	egoFilter []string,
) map[string]any {
	if queryEmbedding == nil {
		return nil
	}
	if minCosine == 0 {
		minCosine = 0.45
	}
	where := `WHERE status = "silent" AND fact_embedding IS NOT NONE`
	params := map[string]any{"vec": queryEmbedding}
	if groupID != nil {
		where += " AND group_id = $group_id"
		params["group_id"] = *groupID
	}
	result, err := driver.Query(ctx,
		"SELECT * FROM relates_to\n"+where+"\n    AND fact_embedding <|4,40|> $vec\nLIMIT 4;",
		params,
	)
	if err != nil {
		return nil
	}
	ego := map[string]struct{}{}
	for _, id := range egoFilter {
		ego[id] = struct{}{}
	}
	for _, row := range UnwrapRows(result) {
		if !EdgePassesFilters(row, filters) {
			continue
		}
		if len(ego) > 0 {
			src := stripRecordID(row["in"])
			dst := stripRecordID(row["out"])
			_, srcOK := ego[src]
			_, dstOK := ego[dst]
			if !srcOK && !dstOK {
				continue
			}
		}
		cos := MemoryCosineSimilarity(rowVector(row["fact_embedding"]), queryEmbedding)
		if cos < minCosine {
			continue
		}
		uid := stringFromAny(row["uuid"])
		if uid == "" {
			continue
		}
		if _, err := driver.Query(ctx,
			`UPDATE relates_to SET status = "active" WHERE uuid = $uuid;`,
			map[string]any{"uuid": uid},
		); err != nil {
			return nil
		}
		row["status"] = "active"
		row["_memory_cosine"] = cos
		row["_memory_resurrected"] = true
		return row
	}
	return nil
}
