package surriti

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
