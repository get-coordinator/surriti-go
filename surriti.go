package surriti

import (
	"context"
	"fmt"
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
	Episodes         []EpisodicNode
	EpisodicEdges    []EpisodicEdge
	Nodes            []EntityNode
	Edges            []EntityEdge
	InvalidatedEdges []EntityEdge
	Communities      []CommunityNode
	CommunityEdges   []CommunityEdge
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

type Surriti struct {
	Driver         Queryer
	LLM            LLMClient
	Embedder       Embedder
	CrossEncoder   CrossEncoder
	Resources      *ResourceStore
	RelationFrames *RelationFrameRegistry

	AliasResolutionEnabled   bool
	AliasResolutionThreshold float64
	AliasResolutionLLM       bool
	ProfileRefreshMode       string
	ProfileSummaryMaxFacts   int
	CognitionConfig          CognitionConfig

	bgCtx              context.Context
	bgCancel           context.CancelFunc
	lifecycle          sync.Mutex
	bgMu               sync.Mutex
	bgWG               sync.WaitGroup
	closed             bool
	cognitionScheduler *CognitionScheduler
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
	if opts.CognitionEnabled != nil {
		cognition.Enabled = *opts.CognitionEnabled
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	frames := opts.RelationFrames
	if frames == nil {
		frames = NewRelationFrameRegistry(seedDefaults, llmFrameClassifier{llm: opts.LLM})
	}
	return &Surriti{
		Driver:                   driver,
		LLM:                      opts.LLM,
		Embedder:                 opts.Embedder,
		CrossEncoder:             opts.CrossEncoder,
		Resources:                NewResourceStore(driver),
		RelationFrames:           frames,
		AliasResolutionEnabled:   aliasResolution,
		AliasResolutionThreshold: threshold,
		AliasResolutionLLM:       aliasLLM,
		ProfileRefreshMode:       profileMode,
		ProfileSummaryMaxFacts:   maxFacts,
		CognitionConfig:          cognition,
		bgCtx:                    bgCtx,
		bgCancel:                 bgCancel,
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
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if c, ok := s.Driver.(interface{ Connect(context.Context) error }); ok {
		if err := c.Connect(ctx); err != nil {
			return nil, err
		}
	}
	if schema, ok := s.Driver.(interface{ InitSchema(context.Context) error }); ok {
		if err := schema.InitSchema(ctx); err != nil {
			return nil, err
		}
	}

	s.bgMu.Lock()
	if s.closed {
		s.bgCtx, s.bgCancel = context.WithCancel(context.Background())
		s.closed = false
	}
	if s.cognitionScheduler == nil {
		s.cognitionScheduler = NewCognitionScheduler(s.Driver, s.LLM, s.Embedder, s.CognitionConfig)
	}
	scheduler := s.cognitionScheduler
	s.bgMu.Unlock()

	scheduler.Start()
	if _, err := scheduler.RecoverPendingEpisodes(ctx); err != nil {
		packageLogf(LogError, "cognition recovery failed: %v", err)
	}
	return s, nil
}

func (s *Surriti) Close(ctx context.Context) error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.bgMu.Lock()
	if !s.closed {
		s.closed = true
	}
	scheduler := s.cognitionScheduler
	s.cognitionScheduler = nil
	s.bgMu.Unlock()

	// Stop cognition before profile jobs and before the shared DB transport.
	// This preserves Python's shutdown ordering and avoids cancelling a
	// SurrealDB request while another goroutine is still consuming replies.
	if scheduler != nil {
		scheduler.Shutdown(ctx)
	}

	done := make(chan struct{})
	go func() {
		s.bgWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.bgCancel()
		<-done
	case <-time.After(2 * time.Second):
		s.bgCancel()
		<-done
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
	return s.SearchCompat(ctx, SearchCompatRequest{Query: query, GroupID: &groupID, Config: config})
}

func (s *Surriti) ExportMemoryPack(ctx context.Context, groupID, outputPath string, includeEmbeddings string, pageSize int) (ExportResult, error) {
	if includeEmbeddings == "" {
		includeEmbeddings = "never"
	}
	if pageSize == 0 {
		pageSize = 1000
	}
	return ExportGroupToZip(ctx, s.Driver, groupID, outputPath, includeEmbeddings, pageSize, nil)
}

func (s *Surriti) ImportMemoryPack(ctx context.Context, inputPath, targetGroupID, mode string) (ImportResult, error) {
	if mode == "" {
		mode = "merge"
	}
	return ImportGroupFromZip(ctx, s.Driver, inputPath, targetGroupID, mode)
}

func (s *Surriti) CognitionScheduler() *CognitionScheduler {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	return s.cognitionScheduler
}
