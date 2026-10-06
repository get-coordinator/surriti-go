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
	return &OpenAILLMClient{Model: model, APIKey: apiKey, BaseURL: openAIBaseURL(baseURL), HTTPClient: &http.Client{Timeout: 10 * time.Minute}, MaxRetries: 2}, nil
}

func (c *OpenAILLMClient) complete(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{"model": c.Model, "temperature": c.Temperature, "response_format": map[string]any{"type": "json_object"}, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}}
	for k, v := range c.ExtraBody {
		payload[k] = v
	}
	raw, err := providerhttp.Post(ctx, c.HTTPClient, c.BaseURL+"/chat/completions", map[string]string{"Authorization": "Bearer " + c.APIKey}, payload, c.MaxRetries, 8<<20)
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
	return providerExtract(ctx, c.complete, req)
}

func (c *OpenAILLMClient) FindContradictions(ctx context.Context, req ContradictionRequest) ([]int, error) {
	return providerFindContradictions(ctx, c.complete, req)
}

func (c *OpenAILLMClient) ClassifyRelationFrame(ctx context.Context, req FrameClassificationRequest) (*RelationFrame, error) {
	return providerClassifyRelationFrame(ctx, c.complete, req)
}

func (c *OpenAILLMClient) Synthesize(ctx context.Context, system, user string) (string, error) {
	return providerSynthesize(ctx, c.complete, system, user)
}

func openAIBaseURL(baseURL string) string {
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_API_BASE")
	}
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return strings.TrimRight(baseURL, "/")
}
