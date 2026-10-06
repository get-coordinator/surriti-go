package surriti

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func buildExtractionUser(req ExtractionRequest) string {
	parts := []string{}
	if strings.TrimSpace(req.Context) != "" {
		parts = append(parts, "CONTEXT (read-only, do NOT extract; use only for pronoun/entity resolution):\n"+strings.TrimSpace(req.Context))
	}
	parts = append(parts, fmt.Sprintf("CURRENT EPISODE (group_id=%q; extract from this only):\n%s", req.GroupID, strings.TrimSpace(req.Content)))
	if len(req.EntityTypes) > 0 {
		keys := sortedEntityTypeKeys(req.EntityTypes)
		parts = append(parts, "Allowed entity types: "+strings.Join(keys, ", "))
	}
	if req.CustomInstructions != "" {
		parts = append(parts, "Additional instructions: "+req.CustomInstructions)
	}
	return strings.Join(parts, "\n\n")
}

var openingJSONFence = regexp.MustCompile("`{3}[a-zA-Z]*\\s*")
var closingJSONFence = regexp.MustCompile("\\s*`{3}\\s*$")

func stripJSONFences(text string) string {
	text = strings.TrimSpace(text)
	if loc := openingJSONFence.FindStringIndex(text); loc != nil {
		text = text[:loc[0]] + text[loc[1]:]
	}
	return strings.TrimSpace(closingJSONFence.ReplaceAllString(text, ""))
}

func stringsFromJSON(v any) []string {
	switch x := v.(type) {
	case nil:
		return []string{}
	case string:
		x = strings.TrimSpace(x)
		if x == "" {
			return []string{}
		}
		return []string{x}
	case []any:
		out := []string{}
		for _, item := range x {
			s := strings.TrimSpace(fmt.Sprint(item))
			if item != nil && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		s := strings.TrimSpace(fmt.Sprint(v))
		if s == "" {
			return []string{}
		}
		return []string{s}
	}
}

func llmFloat(v any, fallback float64) float64 {
	if v == nil {
		return fallback
	}
	if f, ok := toFloat(v); ok {
		return f
	}
	if s, ok := v.(string); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f
		}
	}
	return fallback
}

func parseExtractionJSON(raw string) (ExtractionResult, error) {
	var data map[string]any
	if err := decodeJSONNumbers([]byte(stripJSONFences(raw)), &data); err != nil || data == nil {
		return ExtractionResult{}, fmt.Errorf("%w: LLM did not return valid JSON: %.200q", ErrLLM, raw)
	}
	entities := []ExtractedEntity{}
	if list, ok := data["entities"].([]any); ok {
		for _, item := range list {
			m := mapFromAny(item)
			if len(m) == 0 {
				continue
			}
			names := stringsFromJSON(m["name"])
			labels := asStringSlice(m["labels"])
			if len(labels) == 0 {
				labels = []string{"Entity"}
			}
			for _, name := range names {
				entities = append(entities, ExtractedEntity{Name: name, Summary: stringFromAny(m["summary"]), Labels: append([]string(nil), labels...)})
			}
		}
	}
	facts := []ExtractedFact{}
	allowedClass := map[string]struct{}{"objective": {}, "preference": {}, "style": {}, "constraint": {}, "trait": {}, "sentiment": {}}
	if list, ok := data["facts"].([]any); ok {
		for _, item := range list {
			m := mapFromAny(item)
			if len(m) == 0 {
				continue
			}
			subjects := stringsFromJSON(m["subject"])
			objects := stringsFromJSON(m["object"])
			if len(subjects) == 0 || len(objects) == 0 {
				continue
			}
			predicate := strings.TrimSpace(stringFromAny(m["predicate"]))
			if predicate == "" {
				predicate = "related_to"
			}
			op := FactOperation(strings.ToLower(strings.TrimSpace(stringFromAny(m["operation"]))))
			switch op {
			case FactAssert, FactTerminate, FactCorrect, FactQualify, FactNoop:
			default:
				op = FactAssert
			}
			mc := strings.ToLower(strings.TrimSpace(stringFromAny(m["memory_class"])))
			if _, ok := allowedClass[mc]; !ok {
				mc = "objective"
			}
			var domain *string
			if d, ok := m["domain"].(string); ok {
				d = strings.ToLower(strings.TrimSpace(d))
				if d != "" {
					domain = &d
				}
			}
			replaces := []string{}
			if rawReplaces, ok := m["replaces"].([]any); ok {
				for _, v := range rawReplaces {
					if s := strings.TrimSpace(fmt.Sprint(v)); v != nil && s != "" {
						replaces = append(replaces, s)
					}
				}
			}
			conf := llmFloat(m["confidence"], 1.0)
			var relationPhrase *string
			if v, ok := m["relation_phrase"].(string); ok && strings.TrimSpace(v) != "" {
				v = strings.TrimSpace(v)
				relationPhrase = &v
			}
			qual := mapFromAny(m["qualifiers"])
			if qual == nil {
				qual = map[string]any{}
			}
			roles := map[string]string{}
			for k, v := range mapFromAny(m["argument_roles"]) {
				roles[k] = stringFromAny(v)
			}
			var sourceSpan *string
			if v, ok := m["source_span"].(string); ok && strings.TrimSpace(v) != "" {
				v = strings.TrimSpace(v)
				sourceSpan = &v
			}
			var validAt, invalidAt *string
			if v := stringFromAny(m["valid_at"]); v != "" {
				validAt = &v
			}
			if v := stringFromAny(m["invalid_at"]); v != "" {
				invalidAt = &v
			}
			for _, sub := range subjects {
				for _, obj := range objects {
					factText := strings.TrimSpace(stringFromAny(m["fact"]))
					if !jsonTruthy(m["fact"]) {
						factText = fmt.Sprintf("%s %s %s.", sub, predicate, obj)
					}
					facts = append(facts, ExtractedFact{Subject: sub, Predicate: predicate, Object: obj, Fact: factText, ValidAt: validAt, InvalidAt: invalidAt, Operation: op, Temporal: jsonTruthy(m["temporal"]), Singleton: jsonTruthy(m["singleton"]), Domain: domain, MemoryClass: mc, Replaces: append([]string(nil), replaces...), Confidence: conf, RelationPhrase: relationPhrase, Qualifiers: qual, ArgumentRoles: roles, SourceSpan: sourceSpan})
				}
			}
		}
	}
	return ExtractionResult{Entities: entities, Facts: facts}, nil
}

func buildContradictionUser(req ContradictionRequest) string {
	parts := []string{}
	if req.NewFactStruct != nil {
		f := req.NewFactStruct
		domain := "<none>"
		if f.Domain != nil {
			domain = *f.Domain
		}
		parts = append(parts, fmt.Sprintf("NEW FACT:\n  text:      %s\n  subject:   %s\n  predicate: %s\n  object:    %s\n  domain:    %s\n  operation: %s", req.NewFact, f.Subject, f.Predicate, f.Object, domain, f.Operation))
	} else {
		parts = append(parts, "NEW FACT: "+req.NewFact)
	}
	if len(req.Candidates) > 0 {
		lines := []string{}
		for i, c := range req.Candidates {
			domain := "<none>"
			if c.Domain != nil {
				domain = *c.Domain
			}
			lines = append(lines, fmt.Sprintf("  %d.\n     text:      %s\n     subject:   %s\n     predicate: %s\n     object:    %s\n     domain:    %s", i, c.Fact, c.Subject, c.Predicate, c.Object, domain))
		}
		parts = append(parts, "PRIOR FACTS (indexed):\n"+strings.Join(lines, "\n"))
	} else {
		lines := []string{}
		for i, f := range req.ExistingFacts {
			lines = append(lines, fmt.Sprintf("  %d. %s", i, f))
		}
		parts = append(parts, "PRIOR FACTS (indexed):\n"+strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func parseContradictionsJSON(raw string, n int) []int {
	var data map[string]any
	if decodeJSONNumbers([]byte(stripJSONFences(raw)), &data) != nil {
		return []int{}
	}
	arr := asAnySlice(data["invalidated_indexes"])
	out := []int{}
	for _, x := range arr {
		idx := -1
		switch v := x.(type) {
		case int:
			idx = v
		case json.Number:
			if i, err := strconv.Atoi(v.String()); err == nil {
				idx = i
			}
		case bool:
			if v {
				idx = 1
			} else {
				idx = 0
			}
		}
		if idx >= 0 && idx < n {
			out = append(out, idx)
		}
	}
	return out
}

func buildFrameClassificationUser(req FrameClassificationRequest) string {
	lines := []string{"PREDICATE: " + req.Predicate}
	if req.SourceSpan != "" {
		lines = append(lines, "SOURCE SPAN: "+req.SourceSpan)
	}
	if req.SampleSubject != "" || req.SampleObject != "" {
		sub := req.SampleSubject
		if sub == "" {
			sub = "<subject>"
		}
		obj := req.SampleObject
		if obj == "" {
			obj = "<object>"
		}
		lines = append(lines, fmt.Sprintf("EXAMPLE TRIPLE: (%s) -[%s]-> (%s)", sub, req.Predicate, obj))
	}
	return strings.Join(lines, "\n")
}

func parseFrameClassificationJSON(raw, fallback string) *RelationFrame {
	var data map[string]any
	if decodeJSONNumbers([]byte(stripJSONFences(raw)), &data) != nil {
		return nil
	}
	canon := strings.ToLower(strings.TrimSpace(stringFromAny(data["canonical_name"])))
	if canon == "" {
		canon = strings.ToLower(strings.TrimSpace(fallback))
	}
	if canon == "" {
		return nil
	}
	aliases := []string{}
	for _, a := range stringsFromJSON(data["aliases"]) {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			aliases = append(aliases, a)
		}
	}
	dir := Directionality(strings.ToLower(stringFromAny(data["directionality"])))
	switch dir {
	case DirectionDirected, DirectionSymmetric, DirectionInversePair, DirectionUnknown:
	default:
		dir = DirectionUnknown
	}
	tk := TemporalKind(strings.ToLower(stringFromAny(data["temporal_kind"])))
	switch tk {
	case TemporalState, TemporalEvent, TemporalTimeless, TemporalRecurring, TemporalUnknown:
	default:
		tk = TemporalUnknown
	}
	card := Cardinality(strings.ToLower(stringFromAny(data["cardinality"])))
	switch card {
	case CardinalityOneCurrent, CardinalityManyCurrent, CardinalityManyHistorical, CardinalityTimeless, CardinalityUnknown:
	default:
		card = CardinalityUnknown
	}
	cp := ContradictionPolicy(strings.ToLower(stringFromAny(data["contradiction_policy"])))
	switch cp {
	case ContradictionReplace, ContradictionCoexist, ContradictionNegate, ContradictionUncertain:
	default:
		cp = ContradictionUncertain
	}
	var inv, sr, or *string
	if v := strings.ToLower(strings.TrimSpace(stringFromAny(data["inverse_name"]))); v != "" {
		inv = &v
	}
	if v := strings.TrimSpace(stringFromAny(data["subject_role"])); v != "" {
		sr = &v
	}
	if v := strings.TrimSpace(stringFromAny(data["object_role"])); v != "" {
		or = &v
	}
	conf := llmFloat(data["confidence"], .5)
	if conf < 0 {
		conf = 0
	}
	if conf > 1 {
		conf = 1
	}
	f := RelationFrame{BaseModel: NewBaseModel(""), CanonicalName: canon, Aliases: aliases, Directionality: dir, TemporalKind: tk, Cardinality: card, ContradictionPolicy: cp, InverseName: inv, SubjectRole: sr, ObjectRole: or, Confidence: conf}
	return &f
}

type OpenAILLMClient struct {
	Model       string
	APIKey      string
	BaseURL     string
	Temperature float64
	ExtraBody   map[string]any
	HTTPClient  *http.Client
	MaxRetries  int
}

func NewOpenAILLMClient(model, apiKey, baseURL string) (*OpenAILLMClient, error) {
	if model == "" {
		model = "gpt-4o-mini"
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%w: OPENAI_API_KEY is not set and no api key was provided", ErrConfig)
	}
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_API_BASE")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAILLMClient{Model: model, APIKey: apiKey, BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: &http.Client{Timeout: 10 * time.Minute}, MaxRetries: 2}, nil
}

func (c *OpenAILLMClient) complete(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{"model": c.Model, "temperature": c.Temperature, "response_format": map[string]any{"type": "json_object"}, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}}
	for k, v := range c.ExtraBody {
		payload[k] = v
	}
	raw, err := providerPOST(ctx, c.HTTPClient, c.BaseURL+"/chat/completions", map[string]string{"Authorization": "Bearer " + c.APIKey}, payload, c.MaxRetries, 8<<20)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || len(parsed.Choices) == 0 {
		return "", fmt.Errorf("%w: malformed OpenAI response", ErrLLM)
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}
func (c *OpenAILLMClient) Extract(ctx context.Context, req ExtractionRequest) (ExtractionResult, error) {
	raw, err := c.complete(ctx, ExtractionSystemPrompt, buildExtractionUser(req))
	if err != nil {
		return ExtractionResult{}, err
	}
	return parseExtractionJSON(raw)
}
func (c *OpenAILLMClient) FindContradictions(ctx context.Context, req ContradictionRequest) ([]int, error) {
	if len(req.ExistingFacts) == 0 {
		return []int{}, nil
	}
	raw, err := c.complete(ctx, ContradictionSystemPrompt, buildContradictionUser(req))
	if err != nil {
		return nil, err
	}
	return parseContradictionsJSON(raw, len(req.ExistingFacts)), nil
}
func (c *OpenAILLMClient) ClassifyRelationFrame(ctx context.Context, req FrameClassificationRequest) (*RelationFrame, error) {
	raw, err := c.complete(ctx, FrameClassificationSystemPrompt, buildFrameClassificationUser(req))
	if err != nil {
		return nil, nil
	}
	return parseFrameClassificationJSON(raw, req.Predicate), nil
}
func (c *OpenAILLMClient) Synthesize(ctx context.Context, system, user string) (string, error) {
	raw, err := c.complete(ctx, system, user)
	if err != nil && ctx.Err() == nil {
		packageLogf(LogDebug, "optional synthesis failed: %v", err)
		return "", nil
	}
	return raw, err
}

type AnthropicLLMClient struct {
	Model, APIKey, BaseURL string
	MaxTokens              int
	Temperature            float64
	HTTPClient             *http.Client
	MaxRetries             int
}

func NewAnthropicLLMClient(model, apiKey string) (*AnthropicLLMClient, error) {
	if model == "" {
		model = "claude-3-5-haiku-latest"
	}
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%w: ANTHROPIC_API_KEY is not set and no api key was provided", ErrConfig)
	}
	return &AnthropicLLMClient{Model: model, APIKey: apiKey, BaseURL: "https://api.anthropic.com/v1", MaxTokens: 2048, HTTPClient: &http.Client{Timeout: 10 * time.Minute}, MaxRetries: 2}, nil
}
func supportsAnthropicSampling(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, p := range []string{"claude-fable-", "claude-mythos-", "claude-opus-5", "claude-opus-4-7", "claude-opus-4-8", "claude-sonnet-5"} {
		if strings.HasPrefix(m, p) {
			return false
		}
	}
	return true
}
func (c *AnthropicLLMClient) complete(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{"model": c.Model, "max_tokens": c.MaxTokens, "system": system, "messages": []map[string]string{{"role": "user", "content": user}}}
	if supportsAnthropicSampling(c.Model) {
		payload["temperature"] = c.Temperature
	}
	raw, err := providerPOST(ctx, c.HTTPClient, strings.TrimRight(c.BaseURL, "/")+"/messages", map[string]string{"x-api-key": c.APIKey, "anthropic-version": "2023-06-01"}, payload, c.MaxRetries, 8<<20)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return "", fmt.Errorf("%w: malformed Anthropic response", ErrLLM)
	}
	var b strings.Builder
	for _, x := range parsed.Content {
		b.WriteString(x.Text)
	}
	return strings.TrimSpace(b.String()), nil
}
func (c *AnthropicLLMClient) Extract(ctx context.Context, req ExtractionRequest) (ExtractionResult, error) {
	raw, err := c.complete(ctx, ExtractionSystemPrompt, buildExtractionUser(req))
	if err != nil {
		return ExtractionResult{}, err
	}
	return parseExtractionJSON(raw)
}
func (c *AnthropicLLMClient) FindContradictions(ctx context.Context, req ContradictionRequest) ([]int, error) {
	if len(req.ExistingFacts) == 0 {
		return []int{}, nil
	}
	raw, err := c.complete(ctx, ContradictionSystemPrompt, buildContradictionUser(req))
	if err != nil {
		return nil, err
	}
	return parseContradictionsJSON(raw, len(req.ExistingFacts)), nil
}
func (c *AnthropicLLMClient) ClassifyRelationFrame(ctx context.Context, req FrameClassificationRequest) (*RelationFrame, error) {
	raw, err := c.complete(ctx, FrameClassificationSystemPrompt, buildFrameClassificationUser(req))
	if err != nil {
		return nil, nil
	}
	return parseFrameClassificationJSON(raw, req.Predicate), nil
}
func (c *AnthropicLLMClient) Synthesize(ctx context.Context, system, user string) (string, error) {
	raw, err := c.complete(ctx, system, user)
	if err != nil && ctx.Err() == nil {
		packageLogf(LogDebug, "optional synthesis failed: %v", err)
		return "", nil
	}
	return raw, err
}
