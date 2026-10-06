package surriti

import (
	"context"
	"fmt"
	"strings"
)

// AdvancedSearchRequest is the Go counterpart of Python Surriti.search_().
// Driver is optional; when nil the facade's configured driver is used.
type AdvancedSearchRequest struct {
	Query              string
	Config             *SearchConfig
	SearchConfig       *SearchConfig
	GroupIDs           []string
	GroupID            *string
	CenterNodeUUID     *string
	BFSOriginNodeUUIDs []string // accepted for Graphiti compatibility; current Python baseline also ignores it.
	SearchFilter       *SearchFilters
	Filters            *SearchFilters
	Driver             Queryer
}

// Search_ preserves the advanced Graphiti-compatible search surface from the
// Python implementation. The trailing underscore is intentional.
func (s *Surriti) Search_(ctx context.Context, req AdvancedSearchRequest) (SearchResults, error) {
	if len(req.GroupIDs) > 1 {
		return SearchResults{}, fmt.Errorf("%w: multi-group search is not yet supported; pass one group", ErrConfig)
	}
	cfg := DefaultSearchConfig()
	if req.SearchConfig != nil {
		cfg = *req.SearchConfig
	} else if req.Config != nil {
		cfg = *req.Config
	} else {
		cfg.IncludeNodes = true
		cfg.IncludeEpisodes = true
	}
	if req.Filters != nil {
		cfg.Filters = req.Filters
	} else if req.SearchFilter != nil {
		cfg.Filters = req.SearchFilter
	}
	if req.CenterNodeUUID != nil {
		cfg.FocalUUID = req.CenterNodeUUID
	}
	if cfg.CrossEncoder == nil {
		cfg.CrossEncoder = s.CrossEncoder
	}
	var groupID *string
	if req.GroupID != nil {
		groupID = req.GroupID
	} else if len(req.GroupIDs) == 1 {
		g := req.GroupIDs[0]
		groupID = &g
	}
	driver := req.Driver
	if driver == nil {
		driver = s.Driver
	}
	var embedding []float64
	var err error
	if req.Query != "" {
		embedding, err = s.Embedder.Create(ctx, req.Query)
		if err != nil { return SearchResults{}, err }
	}
	result, err := HybridSearch(ctx, driver, req.Query, embedding, groupID, &cfg, nil, nil)
	if err != nil { return SearchResults{}, err }
	if cfg.IncludeNodes {
		result.Nodes, err = SearchNodes(ctx, driver, req.Query, embedding, groupID, cfg.Limit, cfg.Filters)
		if err != nil { return SearchResults{}, err }
	}
	if cfg.IncludeEpisodes {
		result.Episodes, err = SearchEpisodes(ctx, driver, req.Query, groupID, cfg.Limit)
		if err != nil { return SearchResults{}, err }
	}
	if cfg.IncludeCommunities {
		result.Communities, err = SearchCommunities(ctx, driver, req.Query, embedding, groupID, cfg.Limit)
		if err != nil { return SearchResults{}, err }
	}
	return result, nil
}

// SearchAdvanced is an idiomatic alias for Search_.
func (s *Surriti) SearchAdvanced(ctx context.Context, req AdvancedSearchRequest) (SearchResults, error) {
	return s.Search_(ctx, req)
}

type SearchCompatRequest struct {
	Query            string
	CenterNodeUUID   *string
	GroupIDs         []string
	NumResults       int
	SearchFilter     *SearchFilters
	Driver           Queryer
	GroupID          *string
	Config           *SearchConfig
	Limit            int
	Depth            string
	RerankStrategy   string
	OnlyValid        *bool
	AllowedEdgeUUIDs []string
}

// SearchCompat preserves Python search()'s Graphiti-style argument mapping
// while returning a strongly typed SearchResults. Callers that only need edges
// can use result.Edges.
func (s *Surriti) SearchCompat(ctx context.Context, req SearchCompatRequest) (SearchResults, error) {
	if len(req.GroupIDs) > 1 {
		return SearchResults{}, fmt.Errorf("%w: multi-group search is not yet supported; pass one group", ErrConfig)
	}
	cfg := DefaultSearchConfig()
	if req.Config != nil { cfg = *req.Config }
	if req.NumResults != 0 { cfg.Limit = req.NumResults }
	if req.Limit != 0 { cfg.Limit = req.Limit }
	if req.CenterNodeUUID != nil { cfg.FocalUUID = req.CenterNodeUUID }
	if req.SearchFilter != nil { cfg.Filters = req.SearchFilter }
	if cfg.CrossEncoder == nil { cfg.CrossEncoder = s.CrossEncoder }
	if req.OnlyValid != nil { cfg.OnlyValid = *req.OnlyValid }
	switch Reranker(strings.TrimSpace(req.RerankStrategy)) {
	case RerankRRF, RerankMMR, RerankCrossEncoder, RerankNodeDistance, RerankEpisodeMentions:
		if req.RerankStrategy != "" { cfg.Reranker = Reranker(req.RerankStrategy) }
	}
	switch req.Depth {
	case "fast":
		cfg.Limit = 10
	case "normal":
		cfg.Limit = 25
	case "deep":
		cfg.Limit = 50
	}
	var groupID *string
	if req.GroupID != nil { groupID = req.GroupID } else if len(req.GroupIDs)==1 { g:=req.GroupIDs[0]; groupID=&g }
	driver:=req.Driver;if driver==nil{driver=s.Driver}
	var embedding []float64
	var err error
	if req.Query!=""{embedding,err=s.Embedder.Create(ctx,req.Query);if err!=nil{return SearchResults{},err}}
	return HybridSearch(ctx,driver,req.Query,embedding,groupID,&cfg,nil,req.AllowedEdgeUUIDs)
}

// CreateLoggedTask is the Go fire-and-forget equivalent of Python's
// create_logged_task(): it keeps panic/error reporting explicit instead of
// silently losing background failures.
func CreateLoggedTask(ctx context.Context, name string, fn func(context.Context) error) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("background task %s panicked: %v", name, r)
				packageLogf(LogError, "bg_task.failed name=%s err=%v", name, err)
				done <- err
			}
		}()
		err := fn(ctx)
		if err != nil {
			packageLogf(LogError, "bg_task.failed name=%s err=%v", name, err)
		}
		done <- err
	}()
	return done
}
