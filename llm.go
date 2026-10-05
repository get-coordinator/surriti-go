package surriti

import "context"

type FactOperation string

const (
	FactAssert    FactOperation = "assert"
	FactTerminate FactOperation = "terminate"
	FactCorrect   FactOperation = "correct"
	FactQualify   FactOperation = "qualify"
	FactNoop      FactOperation = "noop"
)

type ExtractedEntity struct {
	Name    string   `json:"name"`
	Summary string   `json:"summary,omitempty"`
	Labels  []string `json:"labels,omitempty"`
}

func NewExtractedEntity(name string) ExtractedEntity {
	return ExtractedEntity{Name: name, Labels: []string{"Entity"}}
}

type ExtractedFact struct {
	Subject        string            `json:"subject"`
	Predicate      string            `json:"predicate"`
	Object         string            `json:"object"`
	Fact           string            `json:"fact,omitempty"`
	ValidAt        *string           `json:"valid_at,omitempty"`
	InvalidAt      *string           `json:"invalid_at,omitempty"`
	Operation      FactOperation     `json:"operation"`
	Temporal       bool              `json:"temporal"`
	Singleton      bool              `json:"singleton"`
	Domain         *string           `json:"domain,omitempty"`
	MemoryClass    string            `json:"memory_class"`
	Replaces       []string          `json:"replaces"`
	Confidence     float64           `json:"confidence"`
	RelationPhrase *string           `json:"relation_phrase,omitempty"`
	Qualifiers     map[string]any    `json:"qualifiers"`
	ArgumentRoles  map[string]string `json:"argument_roles"`
	SourceSpan     *string           `json:"source_span,omitempty"`
}

func NewExtractedFact(subject, predicate, object string) ExtractedFact {
	return ExtractedFact{
		Subject: subject, Predicate: predicate, Object: object,
		Operation: FactAssert, MemoryClass: "objective", Confidence: 1,
		Replaces: []string{}, Qualifiers: map[string]any{}, ArgumentRoles: map[string]string{},
	}
}

type ExtractionResult struct {
	Entities []ExtractedEntity `json:"entities"`
	Facts    []ExtractedFact   `json:"facts"`
}

type ContradictionCandidate struct {
	UUID      string  `json:"uuid"`
	Subject   string  `json:"subject"`
	Predicate string  `json:"predicate"`
	Object    string  `json:"object"`
	Fact      string  `json:"fact"`
	Domain    *string `json:"domain,omitempty"`
	ValidAt   *string `json:"valid_at,omitempty"`
	InvalidAt *string `json:"invalid_at,omitempty"`
}

type ExtractionRequest struct {
	Content            string
	GroupID            string
	EntityTypes        map[string]any
	CustomInstructions string
	Context            string
}

type ContradictionRequest struct {
	NewFact       string
	ExistingFacts []string
	Candidates    []ContradictionCandidate
	NewFactStruct *ExtractedFact
}

// LLMClient is the minimum language-model capability Surriti needs.
// Provider-specific transports belong in adapters, not in the core package.
type LLMClient interface {
	Extract(context.Context, ExtractionRequest) (ExtractionResult, error)
	FindContradictions(context.Context, ContradictionRequest) ([]int, error)
}

// RelationFrameClassifier is an optional capability used for unknown relations.
type RelationFrameClassifier interface {
	ClassifyRelationFrame(context.Context, FrameClassificationRequest) (*RelationFrame, error)
}

// Synthesizer is an optional generic structured-synthesis capability used by
// cognition passes. Implementations may be the same object as LLMClient.
type Synthesizer interface {
	Synthesize(context.Context, string, string) (string, error)
}

type FrameClassificationRequest struct {
	Predicate     string
	SourceSpan    string
	SampleSubject string
	SampleObject  string
}
