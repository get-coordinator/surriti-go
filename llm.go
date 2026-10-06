package surriti

import (
	"context"
	"regexp"
	"strings"
	"sync"
)

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

type LLMClient interface {
	Extract(context.Context, ExtractionRequest) (ExtractionResult, error)
	FindContradictions(context.Context, ContradictionRequest) ([]int, error)
}

type RelationFrameClassifier interface {
	ClassifyRelationFrame(context.Context, FrameClassificationRequest) (*RelationFrame, error)
}

type Synthesizer interface {
	Synthesize(context.Context, string, string) (string, error)
}

type FrameClassificationRequest struct {
	Predicate     string
	SourceSpan    string
	SampleSubject string
	SampleObject  string
}

var dummyEntityRE = regexp.MustCompile(`\b([A-Z][a-zA-Z0-9_-]+(?:[[:space:]]+[A-Z][a-zA-Z0-9_-]+)*)\b`)
var dummyWordRE = regexp.MustCompile(`[A-Za-z0-9_]+`)

var dummyStopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "of": {}, "in": {}, "on": {}, "at": {}, "to": {}, "for": {}, "with": {},
	"this": {}, "that": {}, "these": {}, "those": {}, "it": {}, "its": {}, "from": {}, "as": {}, "by": {},
}

// DummyLLMClient mirrors Python's deterministic, offline heuristic client.
type DummyLLMClient struct{}

func (DummyLLMClient) Extract(_ context.Context, req ExtractionRequest) (ExtractionResult, error) {
	text := req.Content
	allNames := []string{}
	seen := map[string]struct{}{}
	for _, match := range dummyEntityRE.FindAllStringSubmatch(text, -1) {
		name := strings.TrimSpace(match[1])
		if _, stop := dummyStopwords[strings.ToLower(name)]; stop {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		allNames = append(allNames, name)
	}
	entities := make([]ExtractedEntity, 0, len(allNames))
	for _, name := range allNames {
		entities = append(entities, NewExtractedEntity(name))
	}

	facts := []ExtractedFact{}
	for _, sentence := range splitDummySentences(text) {
		present := []string{}
		for _, name := range allNames {
			if strings.Contains(sentence, name) {
				present = append(present, name)
			}
		}
		for i := 0; i+1 < len(present); i++ {
			f := NewExtractedFact(present[i], "related_to", present[i+1])
			f.Fact = ensurePeriod(sentence)
			facts = append(facts, f)
		}
		if len(present) == 1 {
			f := NewExtractedFact(present[0], "MENTIONS_WITH", present[0])
			f.Fact = ensurePeriod(sentence)
			facts = append(facts, f)
		}
	}
	return ExtractionResult{Entities: entities, Facts: facts}, nil
}

func splitDummySentences(text string) []string {
	var out []string
	start := 0
	flush := func(end int) {
		if s := strings.TrimSpace(text[start:end]); s != "" {
			out = append(out, s)
		}
	}
	for i := 0; i < len(text); i++ {
		if text[i] == ';' {
			flush(i)
			start = i + 1
			for start < len(text) && (text[start] == ' ' || text[start] == '\t') {
				start++
			}
			i = start - 1
			continue
		}
		if text[i] == '.' || text[i] == '!' || text[i] == '?' {
			j := i + 1
			if j < len(text) && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r') {
				flush(j)
				for j < len(text) && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n' || text[j] == '\r') {
					j++
				}
				start = j
				i = j - 1
			}
		}
	}
	if start < len(text) {
		flush(len(text))
	}
	return out
}

func ensurePeriod(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ".") {
		return s
	}
	return s + "."
}

func significantTokens(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, tok := range dummyWordRE.FindAllString(strings.ToLower(s), -1) {
		if len(tok) > 2 {
			out[tok] = struct{}{}
		}
	}
	return out
}

func hasTransitionCue(s string) bool {
	s = strings.ToLower(s)
	for _, cue := range []string{"not ", "no longer", "moved", "changed", "stopped", "former"} {
		if strings.Contains(s, cue) {
			return true
		}
	}
	return false
}

func DummyFindContradictions(req ContradictionRequest) []int {
	if !hasTransitionCue(req.NewFact) {
		return []int{}
	}
	newTokens := significantTokens(req.NewFact)
	if len(req.Candidates) > 0 {
		newSubject, newObject := "", ""
		if req.NewFactStruct != nil {
			newSubject = strings.ToLower(req.NewFactStruct.Subject)
			newObject = strings.ToLower(req.NewFactStruct.Object)
		}
		out := []int{}
		for i, cand := range req.Candidates {
			if i >= len(req.ExistingFacts) {
				break
			}
			subjOverlap := newSubject != "" && strings.ToLower(cand.Subject) == newSubject
			objOverlap := newObject != "" && strings.ToLower(cand.Object) == newObject
			shared := 0
			for tok := range significantTokens(cand.Fact) {
				if _, ok := newTokens[tok]; ok {
					shared++
				}
			}
			if subjOverlap || objOverlap || shared >= 1 {
				out = append(out, i)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	out := []int{}
	for i, existing := range req.ExistingFacts {
		shared := 0
		for tok := range significantTokens(existing) {
			if _, ok := newTokens[tok]; ok {
				shared++
			}
		}
		if shared >= 1 {
			out = append(out, i)
		}
	}
	return out
}

func (DummyLLMClient) FindContradictions(_ context.Context, req ContradictionRequest) ([]int, error) {
	return DummyFindContradictions(req), nil
}

func (DummyLLMClient) ClassifyRelationFrame(context.Context, FrameClassificationRequest) (*RelationFrame, error) {
	return nil, nil
}

func (DummyLLMClient) Synthesize(context.Context, string, string) (string, error) {
	return "", nil
}

type ScriptedResponse struct {
	Entities       []ExtractedEntity
	Facts          []ExtractedFact
	Contradictions []int
	Frame          *RelationFrame
}

type ExtractionCall struct {
	Content            string
	Context            string
	GroupID            string
	EntityTypes        []string
	CustomInstructions string
}

type ContradictionCall struct {
	NewFact       string
	ExistingFacts []string
	Candidates    []ContradictionCandidate
	NewFactStruct *ExtractedFact
}

type ClassifyCall struct {
	Predicate     string
	SourceSpan    string
	SampleSubject string
	SampleObject  string
}

// ScriptedLLMClient is concurrency-safe so parallel tests and future callers
// cannot corrupt the deterministic response queue.
type ScriptedLLMClient struct {
	mu                 sync.Mutex
	responses          []ScriptedResponse
	frameQueue         []*RelationFrame
	index              int
	extractCalls       []ExtractionCall
	contradictionCalls []ContradictionCall
	classifyCalls      []ClassifyCall
}

func NewScriptedLLMClient(responses []ScriptedResponse) *ScriptedLLMClient {
	cp := append([]ScriptedResponse(nil), responses...)
	q := []*RelationFrame{}
	for _, r := range cp {
		if r.Frame != nil {
			f := cloneRelationFrame(*r.Frame)
			q = append(q, &f)
		}
	}
	return &ScriptedLLMClient{responses: cp, frameQueue: q}
}

func sortedEntityTypeKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Python preserves dict insertion order. Go maps do not; sorting makes the
	// recorded call deterministic without affecting extraction behavior.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (s *ScriptedLLMClient) Extract(_ context.Context, req ExtractionRequest) (ExtractionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.extractCalls = append(s.extractCalls, ExtractionCall{
		Content: req.Content, Context: req.Context, GroupID: req.GroupID,
		EntityTypes: sortedEntityTypeKeys(req.EntityTypes), CustomInstructions: req.CustomInstructions,
	})
	if s.index >= len(s.responses) {
		return ExtractionResult{Entities: []ExtractedEntity{}, Facts: []ExtractedFact{}}, nil
	}
	resp := s.responses[s.index]
	s.index++
	return ExtractionResult{
		Entities: append([]ExtractedEntity(nil), resp.Entities...),
		Facts:    append([]ExtractedFact(nil), resp.Facts...),
	}, nil
}

func (s *ScriptedLLMClient) FindContradictions(_ context.Context, req ContradictionRequest) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contradictionCalls = append(s.contradictionCalls, ContradictionCall{
		NewFact: req.NewFact, ExistingFacts: append([]string(nil), req.ExistingFacts...),
		Candidates: append([]ContradictionCandidate(nil), req.Candidates...), NewFactStruct: req.NewFactStruct,
	})
	if s.index == 0 || s.index > len(s.responses) {
		return []int{}, nil
	}
	return append([]int(nil), s.responses[s.index-1].Contradictions...), nil
}

func (s *ScriptedLLMClient) ClassifyRelationFrame(_ context.Context, req FrameClassificationRequest) (*RelationFrame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.classifyCalls = append(s.classifyCalls, ClassifyCall{
		Predicate: req.Predicate, SourceSpan: req.SourceSpan,
		SampleSubject: req.SampleSubject, SampleObject: req.SampleObject,
	})
	if len(s.frameQueue) == 0 {
		return nil, nil
	}
	f := cloneRelationFrame(*s.frameQueue[0])
	s.frameQueue = s.frameQueue[1:]
	return &f, nil
}

func (s *ScriptedLLMClient) ExtractCalls() []ExtractionCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExtractionCall(nil), s.extractCalls...)
}

func (s *ScriptedLLMClient) ContradictionCalls() []ContradictionCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ContradictionCall(nil), s.contradictionCalls...)
}

func (s *ScriptedLLMClient) ClassifyCalls() []ClassifyCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ClassifyCall(nil), s.classifyCalls...)
}
