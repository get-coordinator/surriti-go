package surriti

// Search recipe constructors mirror search_recipes.py. Callers receive values,
// not shared pointers, so mutating one configuration never changes another.
func EdgeHybridSearchRRF(limit int) SearchConfig {
	cfg := DefaultSearchConfig()
	if limit != 0 { cfg.Limit = limit }
	cfg.Reranker = RerankRRF
	return cfg
}

func EdgeHybridSearchMMR(limit int) SearchConfig {
	cfg := EdgeHybridSearchRRF(limit)
	cfg.Reranker = RerankMMR
	cfg.MMRLambda = 0.5
	return cfg
}

func EdgeHybridSearchEpisodeMentions(limit int) SearchConfig {
	cfg := EdgeHybridSearchRRF(limit)
	cfg.Reranker = RerankEpisodeMentions
	return cfg
}

func EdgeHybridSearchCrossEncoder(limit int) SearchConfig {
	cfg := EdgeHybridSearchRRF(limit)
	cfg.Reranker = RerankCrossEncoder
	return cfg
}

func EdgeHybridSearchNodeDistance(focalUUID string, limit int) SearchConfig {
	cfg := EdgeHybridSearchRRF(limit)
	cfg.Reranker = RerankNodeDistance
	cfg.FocalUUID = &focalUUID
	return cfg
}

func CombinedHybridSearch(r Reranker, limit int) SearchConfig {
	cfg := DefaultSearchConfig()
	if limit != 0 { cfg.Limit = limit }
	cfg.Reranker = r
	cfg.IncludeNodes = true
	cfg.IncludeEpisodes = true
	cfg.IncludeCommunities = true
	if r == RerankMMR { cfg.MMRLambda = 0.5 }
	return cfg
}

func NodeHybridSearch(r Reranker, limit int) SearchConfig {
	cfg := DefaultSearchConfig()
	if limit != 0 { cfg.Limit = limit }
	cfg.Reranker = r
	cfg.IncludeNodes = true
	if r == RerankMMR { cfg.MMRLambda = 0.5 }
	return cfg
}

func CommunityHybridSearch(r Reranker, limit int) SearchConfig {
	cfg := DefaultSearchConfig()
	if limit != 0 { cfg.Limit = limit }
	cfg.Reranker = r
	cfg.IncludeCommunities = true
	if r == RerankMMR { cfg.MMRLambda = 0.5 }
	return cfg
}

var (
	EdgeHybridSearchRRFDefault             = EdgeHybridSearchRRF(DefaultSearchLimit)
	EdgeHybridSearchMMRDefault             = EdgeHybridSearchMMR(DefaultSearchLimit)
	EdgeHybridSearchEpisodeMentionsDefault = EdgeHybridSearchEpisodeMentions(DefaultSearchLimit)
	EdgeHybridSearchCrossEncoderDefault    = EdgeHybridSearchCrossEncoder(DefaultSearchLimit)

	CombinedHybridSearchRRFDefault          = CombinedHybridSearch(RerankRRF, DefaultSearchLimit)
	CombinedHybridSearchMMRDefault          = CombinedHybridSearch(RerankMMR, DefaultSearchLimit)
	CombinedHybridSearchCrossEncoderDefault = CombinedHybridSearch(RerankCrossEncoder, DefaultSearchLimit)

	NodeHybridSearchRRFDefault             = NodeHybridSearch(RerankRRF, DefaultSearchLimit)
	NodeHybridSearchMMRDefault             = NodeHybridSearch(RerankMMR, DefaultSearchLimit)
	NodeHybridSearchEpisodeMentionsDefault = NodeHybridSearch(RerankEpisodeMentions, DefaultSearchLimit)
	NodeHybridSearchCrossEncoderDefault    = NodeHybridSearch(RerankCrossEncoder, DefaultSearchLimit)

	CommunityHybridSearchRRFDefault          = CommunityHybridSearch(RerankRRF, DefaultSearchLimit)
	CommunityHybridSearchMMRDefault          = CommunityHybridSearch(RerankMMR, DefaultSearchLimit)
	CommunityHybridSearchCrossEncoderDefault = CommunityHybridSearch(RerankCrossEncoder, DefaultSearchLimit)
)
