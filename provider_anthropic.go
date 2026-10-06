package surriti

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/get-coordinator/surriti-go/internal/providerhttp"
)

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
	raw, err := providerhttp.Post(ctx, c.HTTPClient, strings.TrimRight(c.BaseURL, "/")+"/messages", map[string]string{"x-api-key": c.APIKey, "anthropic-version": "2023-06-01"}, payload, c.MaxRetries, 8<<20)
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
	return providerExtract(ctx, c.complete, req)
}

func (c *AnthropicLLMClient) FindContradictions(ctx context.Context, req ContradictionRequest) ([]int, error) {
	return providerFindContradictions(ctx, c.complete, req)
}

func (c *AnthropicLLMClient) ClassifyRelationFrame(ctx context.Context, req FrameClassificationRequest) (*RelationFrame, error) {
	return providerClassifyRelationFrame(ctx, c.complete, req)
}

func (c *AnthropicLLMClient) Synthesize(ctx context.Context, system, user string) (string, error) {
	return providerSynthesize(ctx, c.complete, system, user)
}
