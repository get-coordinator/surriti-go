package surriti

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const Version = "0.5.0"

type AddEpisodeResults struct {
	Episode          EpisodicNode
	EpisodicEdges    []EpisodicEdge
	Nodes            []EntityNode
	Edges            []EntityEdge
	InvalidatedEdges []EntityEdge
	Communities      []CommunityNode
	CommunityEdges   []CommunityEdge
}

type AddBulkEpisodeResults struct {
	Episodes          []EpisodicNode
	EpisodicEdges     []EpisodicEdge
	Nodes             []EntityNode
	Edges             []EntityEdge
	InvalidatedEdges  []EntityEdge
	Communities       []CommunityNode
	CommunityEdges    []CommunityEdge
}

type AddTripletResults struct {
	Nodes            []EntityNode
	Edges            []EntityEdge
	InvalidatedEdges []EntityEdge
}

type RawEpisode struct {
	Name              string
	Content           string
	Source            EpisodeType
	SourceDescription string
	ReferenceTime     *time.Time
	GroupID           *string
	UUID              *string
}

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

type SurritiOptions struct {
	LLM                      LLMClient
	Embedder                 Embedder
	CrossEncoder             CrossEncoder
	RelationFrames           *RelationFrameRegistry
	SeedDefaultFrames        *bool
	AliasResolution          *bool
	AliasResolutionThreshold float64
	AliasResolutionLLM       *bool
	ProfileRefresh           string
	ProfileSummaryMaxFacts   int
	Cognition                  *CognitionConfig
}

type Surriti struct {
	Driver Queryer
	LLM LLMClient
	Embedder Embedder
	CrossEncoder CrossEncoder
	Resources *ResourceStore
	RelationFrames *RelationFrameRegistry

	AliasResolutionEnabled bool
	AliasResolutionThreshold float64
	AliasResolutionLLM bool
	ProfileRefreshMode string
	ProfileSummaryMaxFacts int
	CognitionConfig CognitionConfig

	bgCtx context.Context
	bgCancel context.CancelFunc
	bgMu sync.Mutex
	bgWG sync.WaitGroup
	closed bool
}

type llmFrameClassifier struct{ llm LLMClient }

func (a llmFrameClassifier) ClassifyRelationFrame(ctx context.Context, req FrameClassificationRequest) (*RelationFrame, error) {
	if c, ok := a.llm.(RelationFrameClassifier); ok {
		return c.ClassifyRelationFrame(ctx, req)
	}
	return nil, nil
}

func NewSurriti(driver Queryer, options *SurritiOptions) (*Surriti, error) {
	if driver == nil {
		return nil, fmt.Errorf("%w: nil driver", ErrConfig)
	}
	opts := SurritiOptions{}
	if options != nil {
		opts = *options
	}
	if opts.LLM == nil {
		opts.LLM = DummyLLMClient{}
	}
	if opts.Embedder == nil {
		dim := 768
		if d, ok := driver.(*SurrealDriver); ok {
			dim = d.Config().EmbeddingDim
		}
		opts.Embedder = NewDummyEmbedder(dim)
	}
	seedDefaults := true
	if opts.SeedDefaultFrames != nil {
		seedDefaults = *opts.SeedDefaultFrames
	}
	aliasResolution := true
	if opts.AliasResolution != nil {
		aliasResolution = *opts.AliasResolution
	}
	aliasLLM := true
	if opts.AliasResolutionLLM != nil {
		aliasLLM = *opts.AliasResolutionLLM
	}
	threshold := opts.AliasResolutionThreshold
	if threshold == 0 {
		threshold = .86
	}
	profileMode := opts.ProfileRefresh
	if profileMode == "" {
		profileMode = "async"
	}
	if profileMode != "sync" && profileMode != "async" && profileMode != "off" {
		return nil, fmt.Errorf("%w: profile_refresh must be one of 'sync', 'async', 'off'", ErrConfig)
	}
	maxFacts := opts.ProfileSummaryMaxFacts
	if maxFacts == 0 {
		maxFacts = 30
	}
	cognition := DefaultCognitionConfig()
	if opts.Cognition != nil {
		cognition = *opts.Cognition
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	frames := opts.RelationFrames
	if frames == nil {
		frames = NewRelationFrameRegistry(seedDefaults, llmFrameClassifier{llm: opts.LLM})
	}
	return &Surriti{
		Driver: driver,
		LLM: opts.LLM,
		Embedder: opts.Embedder,
		CrossEncoder: opts.CrossEncoder,
		Resources: NewResourceStore(driver),
		RelationFrames: frames,
		AliasResolutionEnabled: aliasResolution,
		AliasResolutionThreshold: threshold,
		AliasResolutionLLM: aliasLLM,
		ProfileRefreshMode: profileMode,
		ProfileSummaryMaxFacts: maxFacts,
		CognitionConfig: cognition,
		bgCtx: bgCtx,
		bgCancel: bgCancel,
	}, nil
}

func NewSurritiFromEnv(options *SurritiOptions) (*Surriti, error) {
	driver, err := NewSurrealDriverFromEnv()
	if err != nil {
		return nil, err
	}
	return NewSurriti(driver, options)
}

func (s *Surriti) Connect(ctx context.Context) (*Surriti, error) {
	if c, ok := s.Driver.(interface{ Connect(context.Context) error }); ok {
		if err := c.Connect(ctx); err != nil { return nil, err }
	}
	if schema, ok := s.Driver.(interface{ InitSchema(context.Context) error }); ok {
		if err := schema.InitSchema(ctx); err != nil { return nil, err }
	}
	return s, nil
}

func (s *Surriti) Close(ctx context.Context) error {
	s.bgMu.Lock()
	if !s.closed {
		s.closed = true
	}
	s.bgMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.bgCancel()
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	s.bgCancel()
	if c, ok := s.Driver.(interface{ Close(context.Context) error }); ok {
		return c.Close(ctx)
	}
	return nil
}

func (s *Surriti) runBackground(fn func(context.Context)) {
	s.bgMu.Lock()
	if s.closed {
		s.bgMu.Unlock()
		return
	}
	s.bgWG.Add(1)
	ctx := s.bgCtx
	s.bgMu.Unlock()
	go func() {
		defer s.bgWG.Done()
		fn(ctx)
	}()
}

func (s *Surriti) BuildIndicesAndConstraints(ctx context.Context) error {
	if schema, ok := s.Driver.(interface{ InitSchema(context.Context) error }); ok {
		return schema.InitSchema(ctx)
	}
	return nil
}

func (s *Surriti) UpsertResource(ctx context.Context, resource Resource, groupID string) (Resource, error) {
	return s.Resources.Upsert(ctx, resource, groupID)
}

func (s *Surriti) ListResources(ctx context.Context, groupID string, limit int) ([]Resource, error) {
	return s.Resources.List(ctx, groupID, limit)
}

func (s *Surriti) SetResourceAvailability(ctx context.Context, libraryItemID, groupID string, available bool) (bool, error) {
	return s.Resources.SetAvailability(ctx, libraryItemID, groupID, available)
}

func (s *Surriti) Search(ctx context.Context, query, groupID string, config *SearchConfig) (SearchResults, error) {
	emb, err := s.Embedder.Create(ctx, query)
	if err != nil { return SearchResults{}, err }
	cfg := normalizeSearchConfig(config)
	if cfg.CrossEncoder == nil {
		cfg.CrossEncoder = s.CrossEncoder
	}
	g := groupID
	out, err := HybridSearch(ctx, s.Driver, query, emb, &g, &cfg, nil, nil)
	if err != nil { return SearchResults{}, err }
	if cfg.IncludeNodes {
		out.Nodes, err = SearchNodes(ctx, s.Driver, query, emb, &g, cfg.Limit, cfg.Filters)
		if err != nil { return SearchResults{}, err }
	}
	if cfg.IncludeEpisodes {
		out.Episodes, err = SearchEpisodes(ctx, s.Driver, query, &g, cfg.Limit)
		if err != nil { return SearchResults{}, err }
	}
	if cfg.IncludeCommunities {
		out.Communities, err = SearchCommunities(ctx, s.Driver, query, emb, &g, cfg.Limit)
		if err != nil { return SearchResults{}, err }
	}
	return out, nil
}

func (s *Surriti) RegisterFrame(frame RelationFrame, groupID *string) RelationFrame {
	g := ""
	if groupID != nil { g = *groupID }
	return s.RelationFrames.Register(frame, g)
}

func (s *Surriti) GetFrame(predicate, groupID string) (*RelationFrame, bool) {
	f, ok := s.RelationFrames.Get(predicate, groupID)
	if !ok { return nil, false }
	return &f, true
}

func (s *Surriti) MergeFrames(source, target string, groupID *string, strategy string) (RelationFrame, error) {
	if strategy == "" { strategy = "alias" }
	if strategy != "alias" {
		return RelationFrame{}, fmt.Errorf("unsupported merge strategy %q; only 'alias' is implemented", strategy)
	}
	g := ""
	if groupID != nil { g = *groupID }
	src, ok := s.RelationFrames.Get(source, g)
	if !ok { return RelationFrame{}, fmt.Errorf("%w: merge_frames unknown source frame %q", ErrNotFound, source) }
	tgt, ok := s.RelationFrames.Get(target, g)
	if !ok { return RelationFrame{}, fmt.Errorf("%w: merge_frames unknown target frame %q", ErrNotFound, target) }
	if src.CanonicalName == tgt.CanonicalName {
		return tgt, nil
	}
	existing := map[string]struct{}{}
	for _, a := range tgt.Aliases { existing[strings.ToLower(a)] = struct{}{} }
	aliases := append([]string(nil), tgt.Aliases...)
	for _, cand := range append([]string{src.CanonicalName}, src.Aliases...) {
		key := lowerTrim(cand)
		if key == "" || key == lowerTrim(tgt.CanonicalName) { continue }
		if _, seen := existing[key]; seen { continue }
		existing[key] = struct{}{}
		aliases = append(aliases, key)
	}
	tgt.Aliases = aliases
	s.RelationFrames.Register(tgt, g)
	return tgt, nil
}

func (s *Surriti) GetConflicts(ctx context.Context, groupID string, limit int) ([]EntityEdge, error) {
	if limit == 0 { limit = 100 }
	result, err := s.Driver.Query(ctx,
		`SELECT * FROM relates_to WHERE group_id = $group_id AND status = "needs_resolution" LIMIT $limit;`,
		map[string]any{"group_id": groupID, "limit": limit},
	)
	if err != nil { return nil, err }
	rows := UnwrapRows(result)
	out := make([]EntityEdge, 0, len(rows))
	for _, row := range rows { out = append(out, ParseEdge(row)) }
	return out, nil
}

func (s *Surriti) GetCurrentFacts(ctx context.Context, subjectUUID, groupID string, predicate, domain *string, limit int) ([]EntityEdge, error) {
	if limit == 0 { limit = 50 }
	clauses := []string{
		"group_id = $group_id",
		`in = type::record("entity", $src)`,
		`status = "active"`,
		"invalid_at IS NONE",
	}
	params := map[string]any{"group_id": groupID, "src": subjectUUID, "limit": limit}
	if predicate != nil { clauses = append(clauses, "name = $name"); params["name"] = *predicate }
	if domain != nil { clauses = append(clauses, "domain = $domain"); params["domain"] = *domain }
	q := "SELECT * FROM relates_to WHERE "+strings.Join(clauses," AND ")+" ORDER BY valid_at DESC LIMIT $limit;"
	result, err := s.Driver.Query(ctx, q, params)
	if err != nil { return nil, err }
	rows := UnwrapRows(result)
	out := make([]EntityEdge, 0, len(rows))
	for _, row := range rows { out = append(out, ParseEdge(row)) }
	return out, nil
}

func (s *Surriti) GetCurrentFact(ctx context.Context, subjectUUID, predicate, groupID string) (*EntityEdge, error) {
	edges, err := s.GetCurrentFacts(ctx, subjectUUID, groupID, &predicate, nil, 1)
	if err != nil || len(edges)==0 { return nil, err }
	return &edges[0], nil
}

func (s *Surriti) CurrentProfile(ctx context.Context, subjectUUID, groupID string, limit int) (map[string][]EntityEdge, error) {
	if limit == 0 { limit = 200 }
	edges, err := s.GetCurrentFacts(ctx, subjectUUID, groupID, nil, nil, limit)
	if err != nil { return nil, err }
	out := map[string][]EntityEdge{}
	for _, edge := range edges {
		key := edge.CanonicalName
		if key == "" { key = edge.Name }
		out[key] = append(out[key], edge)
	}
	return out, nil
}

func (s *Surriti) GetFactsAsOf(ctx context.Context, subjectUUID string, asOf time.Time, groupID string, predicate, domain *string, limit int) ([]EntityEdge, error) {
	if limit == 0 { limit = 200 }
	clauses := []string{
		"group_id = $group_id",
		`in = type::record("entity", $src)`,
		"(valid_at IS NONE OR valid_at <= $as_of)",
		"(invalid_at IS NONE OR invalid_at > $as_of)",
		"(expired_at IS NONE OR expired_at > $as_of)",
	}
	params := map[string]any{"group_id":groupID,"src":subjectUUID,"as_of":asOf,"limit":limit}
	if predicate != nil { clauses=append(clauses,"name = $name"); params["name"]=*predicate }
	if domain != nil { clauses=append(clauses,"domain = $domain"); params["domain"]=*domain }
	q := "SELECT * FROM relates_to WHERE "+strings.Join(clauses," AND ")+" ORDER BY valid_at DESC LIMIT $limit;"
	result, err := s.Driver.Query(ctx,q,params)
	if err != nil { return nil,err }
	rows:=UnwrapRows(result)
	out:=make([]EntityEdge,0,len(rows))
	for _,row:=range rows { out=append(out,ParseEdge(row)) }
	return out,nil
}

func (s *Surriti) GetStateAsOf(ctx context.Context, subjectUUID string, asOf time.Time, groupID string, predicate, domain *string) (map[string]EntityEdge,error) {
	edges,err:=s.GetFactsAsOf(ctx,subjectUUID,asOf,groupID,predicate,domain,200)
	if err!=nil{return nil,err}
	out:=map[string]EntityEdge{}
	for _,edge:=range edges {
		key:=edge.Name+"\x1f"+edge.TargetNodeUUID
		existing,ok:=out[key]
		if !ok {
			out[key]=edge
			continue
		}
		ev:=time.Time{}; if edge.ValidAt!=nil { ev=*edge.ValidAt }
		xv:=time.Time{}; if existing.ValidAt!=nil { xv=*existing.ValidAt }
		if ev.After(xv) { out[key]=edge }
	}
	return out,nil
}

func sortedStringsUnique(values []string) []string {
	set:=map[string]struct{}{}
	for _,v:=range values { set[v]=struct{}{} }
	out:=make([]string,0,len(set))
	for v:=range set { out=append(out,v) }
	sort.Strings(out)
	return out
}
