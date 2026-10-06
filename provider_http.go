package surriti

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The Python provider SDKs retry transient transport errors and 408/409/429/5xx
// responses twice. Keep that policy at the adapter boundary, never around graph
// ingest, where replay could duplicate writes.
func providerPOST(ctx context.Context, client *http.Client, url string, headers map[string]string, payload any, retries int, maxBytes int64) ([]byte, error) {
	if retries < 0 {
		return nil, fmt.Errorf("%w: max retries must be non-negative", ErrConfig)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: encode provider request: %w", ErrLLM, err)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("%w: provider request: %w", ErrLLM, err)
		}
		req.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		resp, callErr := client.Do(req)
		var raw []byte
		retry := callErr != nil
		var responseHeaders http.Header
		if resp != nil {
			responseHeaders = resp.Header
			raw, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
			resp.Body.Close()
			if callErr == nil {
				callErr = err
			}
			if int64(len(raw)) > maxBytes {
				return nil, fmt.Errorf("%w: provider response exceeds %d bytes", ErrLLM, maxBytes)
			}
			if callErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return raw, nil
			}
			retry = callErr != nil || resp.StatusCode == 408 || resp.StatusCode == 409 || resp.StatusCode == 429 || resp.StatusCode >= 500
			switch resp.Header.Get("x-should-retry") {
			case "true":
				retry = true
			case "false":
				retry = false
			}
			if callErr == nil {
				callErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
			}
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: provider request: %w", ErrLLM, ctx.Err())
		}
		if !retry || attempt >= retries {
			return nil, fmt.Errorf("%w: provider request: %w", ErrLLM, callErr)
		}
		timer := time.NewTimer(providerRetryDelay(responseHeaders, attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%w: provider retry: %w", ErrLLM, ctx.Err())
		case <-timer.C:
		}
	}
}

func providerRetryDelay(headers http.Header, attempt int) time.Duration {
	seconds, err := strconv.ParseFloat(headers.Get("retry-after-ms"), 64)
	if err == nil {
		seconds /= 1000
	} else {
		seconds, err = strconv.ParseFloat(headers.Get("retry-after"), 64)
		if err != nil {
			if date, e := http.ParseTime(headers.Get("retry-after")); e == nil {
				seconds = time.Until(date).Seconds()
			}
		}
	}
	if seconds > 0 && seconds <= 60 {
		return time.Duration(seconds * float64(time.Second))
	}
	return time.Duration(math.Min(8, .5*math.Pow(2, float64(min(attempt, 10)))) * (1 - .25*rand.Float64()) * float64(time.Second))
}
