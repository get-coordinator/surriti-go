package surriti

// SurritiOptions configures providers and memory processing at construction.
// Nil pointer fields retain defaults; non-nil pointers allow explicit false values.
// Start cognition customizations with DefaultCognitionConfig.
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
	Cognition                *CognitionConfig
	// CognitionEnabled mirrors Python's cognition=true/false shorthand.
	// When non-nil it overrides Cognition.Enabled.
	CognitionEnabled *bool
}
