package surriti

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/get-coordinator/surriti-go/internal/providerhttp"
)

type OpenAIEmbedder struct {
	Model      string
	Dim        int
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	MaxRetries int
}

func NewOpenAIEmbedder(model string, dim int, apiKey, baseURL string) (*OpenAIEmbedder, error) {
	if model == "" {
		model = "text-embedding-3-small"
	}
	if dim == 0 {
		dim = 1536
	}
	if dim <= 0 {
		return nil, fmt.Errorf("%w: embedding dimension must be positive", ErrConfig)
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		apiKey = "EMPTY"
	}
	return &OpenAIEmbedder{Model: model, Dim: dim, APIKey: apiKey, BaseURL: openAIBaseURL(baseURL), HTTPClient: &http.Client{Timeout: 10 * time.Minute}, MaxRetries: 2}, nil
}

func (e *OpenAIEmbedder) EmbeddingDim() int { return e.Dim }

func (e *OpenAIEmbedder) Create(ctx context.Context, input string) ([]float64, error) {
	out, err := e.CreateBatch(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: embedding response has no data", ErrLLM)
	}
	return out[0], nil
}

func (e *OpenAIEmbedder) CreateBatch(ctx context.Context, input []string) ([][]float64, error) {
	if len(input) == 0 {
		return [][]float64{}, nil
	}
	payload := map[string]any{"model": e.Model, "input": input, "dimensions": e.Dim}
	raw, err := providerhttp.Post(ctx, e.HTTPClient, e.BaseURL+"/embeddings", map[string]string{"Authorization": "Bearer " + e.APIKey}, payload, e.MaxRetries, 16<<20)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("%w: malformed embedding response: %v", ErrLLM, err)
	}
	out := make([][]float64, len(parsed.Data))
	for i, item := range parsed.Data {
		out[i] = item.Embedding
	}
	return out, nil
}

// Close releases idle HTTP connections. Calls in progress remain owned by their contexts.
func (e *OpenAIEmbedder) Close() error {
	if e.HTTPClient != nil {
		e.HTTPClient.CloseIdleConnections()
	}
	return nil
}
