package surriti

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderRetriesTransientResponses(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing authorization")
		}
		if calls.Add(1) < 3 {
			w.Header().Set("retry-after-ms", "1")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"done"}}]}`))
	}))
	defer server.Close()
	client, err := NewOpenAILLMClient("model", "test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Synthesize(context.Background(), "system", "user")
	if err != nil || result != "done" || calls.Load() != 3 {
		t.Fatalf("result=%q calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestProviderCancellationDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("retry-after", "60")
		w.WriteHeader(503)
		cancel()
	}))
	defer server.Close()
	_, err := providerPOST(ctx, server.Client(), server.URL, nil, map[string]any{}, 2, 1024)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestProviderRejectsOversizedResponsesAndInvalidRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(`{"oversized":true}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := providerPOST(ctx, server.Client(), server.URL, nil, nil, 2, 4); !errors.Is(err, ErrLLM) {
		t.Fatalf("err=%v", err)
	}
	if _, err := providerPOST(ctx, server.Client(), server.URL, nil, make(chan int), 2, 1024); !errors.Is(err, ErrLLM) {
		t.Fatalf("err=%v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
}
