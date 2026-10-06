package surriti

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCognitionManualPassOwnedByShutdown(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	driver := queryFunc(func(ctx context.Context, _ string, _ map[string]any) (any, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	})
	scheduler := NewCognitionScheduler(driver, DummyLLMClient{}, NewDummyEmbedder(8), DefaultCognitionConfig())
	done := make(chan struct{})
	go func() { defer close(done); scheduler.RunOnce(context.Background(), "g", []string{"episode"}) }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scheduler.Shutdown(ctx)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown left manual pass running")
	}
	scheduler.Start()
	scheduler.Shutdown(context.Background())
}

func TestCognitionFailedBatchReturnsToDebounce(t *testing.T) {
	driver := queryFunc(func(context.Context, string, map[string]any) (any, error) { return nil, errors.New("offline") })
	scheduler := NewCognitionScheduler(driver, DummyLLMClient{}, NewDummyEmbedder(8), DefaultCognitionConfig())
	metrics := scheduler.RunOnce(context.Background(), "g", []string{"episode"})
	if metrics.Processed || len(metrics.FailedSteps) == 0 {
		t.Fatal("failed pass marked processed")
	}
	scheduler.mu.Lock()
	state := scheduler.states["g"]
	delay := scheduler.delayLocked(state)
	pending := append([]string(nil), state.PendingEpisodeUUIDs...)
	passes := state.PassCount
	scheduler.mu.Unlock()
	if delay != 8*time.Second || len(pending) != 1 || pending[0] != "episode" || passes != 1 {
		t.Fatalf("delay=%v pending=%v passes=%d", delay, pending, passes)
	}
	scheduler.Shutdown(context.Background())
}
