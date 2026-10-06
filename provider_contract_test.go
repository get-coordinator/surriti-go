package surriti_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	surriti "github.com/get-coordinator/surriti-go"
)

type modelProvider interface {
	surriti.LLMClient
	surriti.RelationFrameClassifier
	surriti.Synthesizer
}

// Exercise the exported adapters over HTTP so shared operation behavior cannot
// drift between providers during refactors.
func TestProviderContract(t *testing.T) {
	for _, vendor := range []string{"openai", "anthropic"} {
		t.Run(vendor, func(t *testing.T) {
			content := ""
			status := http.StatusOK
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if payload["model"] != "test-model" || r.Method != http.MethodPost {
					t.Error("invalid model request")
				}
				if vendor == "openai" {
					if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
						t.Error("invalid OpenAI request")
					}
				} else if r.URL.Path != "/messages" || r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") != "2023-06-01" {
					t.Error("invalid Anthropic request")
				}
				w.WriteHeader(status)
				if vendor == "openai" {
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"content": []any{map[string]any{"text": content}}})
				}
			}))
			defer server.Close()
			var client modelProvider
			if vendor == "openai" {
				c, err := surriti.NewOpenAILLMClient("test-model", "test-key", server.URL)
				if err != nil {
					t.Fatal(err)
				}
				c.HTTPClient, c.MaxRetries = server.Client(), 0
				client = c
			} else {
				c, err := surriti.NewAnthropicLLMClient("test-model", "test-key")
				if err != nil {
					t.Fatal(err)
				}
				c.BaseURL, c.HTTPClient, c.MaxRetries = server.URL, server.Client(), 0
				client = c
			}
			ctx := context.Background()
			content = `{"entities":[{"name":"Alice"}],"facts":[{"subject":"Alice","predicate":"lives_in","object":"Paris"}]}`
			extraction, err := client.Extract(ctx, surriti.ExtractionRequest{Content: "Alice lives in Paris", GroupID: "g"})
			if err != nil || len(extraction.Entities) != 1 || len(extraction.Facts) != 1 || extraction.Facts[0].Object != "Paris" {
				t.Fatalf("extraction=%+v err=%v", extraction, err)
			}
			content = `{"invalidated_indexes":[0,9]}`
			indexes, err := client.FindContradictions(ctx, surriti.ContradictionRequest{NewFact: "Alice lives in Paris", ExistingFacts: []string{"Alice lives in Rome"}})
			if err != nil || fmt.Sprint(indexes) != "[0]" {
				t.Fatalf("indexes=%v err=%v", indexes, err)
			}
			before := calls
			indexes, err = client.FindContradictions(ctx, surriti.ContradictionRequest{})
			if err != nil || len(indexes) != 0 || calls != before {
				t.Fatalf("empty contradictions made a request: %v", err)
			}
			content = `{"canonical_name":"lives_in","directionality":"directed"}`
			frame, err := client.ClassifyRelationFrame(ctx, surriti.FrameClassificationRequest{Predicate: "lives_in"})
			if err != nil || frame == nil || frame.CanonicalName != "lives_in" {
				t.Fatalf("frame=%+v err=%v", frame, err)
			}
			content = " summary "
			summary, err := client.Synthesize(ctx, "system", "user")
			if err != nil || summary != "summary" {
				t.Fatalf("summary=%q err=%v", summary, err)
			}

			status = http.StatusBadRequest
			if _, err := client.Extract(ctx, surriti.ExtractionRequest{}); !errors.Is(err, surriti.ErrLLM) {
				t.Fatalf("required extraction error=%v", err)
			}
			if _, err := client.FindContradictions(ctx, surriti.ContradictionRequest{ExistingFacts: []string{"old"}}); !errors.Is(err, surriti.ErrLLM) {
				t.Fatalf("required contradiction error=%v", err)
			}
			if frame, err := client.ClassifyRelationFrame(ctx, surriti.FrameClassificationRequest{}); frame != nil || err != nil {
				t.Fatalf("optional classification=%v err=%v", frame, err)
			}
			if summary, err := client.Synthesize(ctx, "system", "user"); summary != "" || err != nil {
				t.Fatalf("optional synthesis=%q err=%v", summary, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := client.Synthesize(cancelled, "system", "user"); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation=%v", err)
			}
		})
	}
}

func TestOpenAIEmbeddingContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("invalid embedding request")
		}
		var payload struct {
			Model      string   `json:"model"`
			Dimensions int      `json:"dimensions"`
			Input      []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "test-model" || payload.Dimensions != 2 || len(payload.Input) != 1 || payload.Input[0] != "hello" {
			t.Errorf("payload=%+v", payload)
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.25,0.75]}]}`))
	}))
	defer server.Close()
	embedder, err := surriti.NewOpenAIEmbedder("test-model", 2, "test-key", server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	defer embedder.Close()
	vector, err := embedder.Create(context.Background(), "hello")
	if err != nil || fmt.Sprint(vector) != "[0.25 0.75]" {
		t.Fatalf("vector=%v err=%v", vector, err)
	}
	if _, err := surriti.NewOpenAIEmbedder("", -1, "test-key", server.URL); !errors.Is(err, surriti.ErrConfig) {
		t.Fatalf("config error=%v", err)
	}
}
