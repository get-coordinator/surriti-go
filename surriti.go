package surriti

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const Version = "0.5.0"

// Surriti coordinates graph memory, providers, and background work. Construct it
// with NewSurriti and configure its exported fields before concurrent use.
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
	frameMu            sync.Mutex
	savedFrames        map[string]RelationFrame
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

// NewSurriti constructs a memory client without connecting. Nil options select
// default settings and deterministic dummy providers. Close closes the supplied
// driver, but does not close injected provider clients.
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

// NewSurritiFromEnv constructs a client using SURRITI_SURREAL_* configuration.
func NewSurritiFromEnv(options *SurritiOptions) (*Surriti, error) {
	driver, err := NewSurrealDriverFromEnv()
	if err != nil {
		return nil, err
	}
	return NewSurriti(driver, options)
}

// Connect connects the driver, initializes the schema, and starts cognition.
func (s *Surriti) Connect(ctx context.Context) (*Surriti, error) {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	if c, ok := s.Driver.(interface{ Connect(context.Context) error }); ok {
		if err := c.Connect(ctx); err != nil {
			return nil, err
		}
	}
	if err := s.BuildIndicesAndConstraints(ctx); err != nil {
		return nil, err
	}

	if err := s.loadFrames(ctx); err != nil {
		return nil, err
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

// Close drains background work before closing the driver. Injected providers
// must honor cancellation so background work can finish.
func (s *Surriti) Close(ctx context.Context) error {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.bgMu.Lock()
	s.closed = true
	scheduler := s.cognitionScheduler
	s.cognitionScheduler = nil
	s.bgMu.Unlock()

	// Stop cognition before profile jobs and before the shared DB transport.
	// This preserves Python's shutdown ordering and avoids cancelling a
	// SurrealDB request while another goroutine is still consuming replies.
	if scheduler != nil {
		scheduler.Shutdown(ctx)
	}

	drainBackground(ctx, &s.bgWG, s.bgCancel)
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

func (s *Surriti) CognitionScheduler() *CognitionScheduler {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	return s.cognitionScheduler
}

// Callers must prevent new work before draining. Cancellation requests a stop;
// waiting afterward guarantees no worker uses the driver after it is closed.
func drainBackground(ctx context.Context, workers *sync.WaitGroup, cancel context.CancelFunc) {
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-ctx.Done():
	case <-timer.C:
	}
	cancel()
	<-done
}
