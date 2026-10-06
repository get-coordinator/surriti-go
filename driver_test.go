package surriti

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeFactory struct {
	mu      sync.Mutex
	opens   int
	openFn  func(context.Context, string, int) (DBClient, error)
	clients []DBClient
}

func (f *fakeFactory) Open(ctx context.Context, url string) (DBClient, error) {
	f.mu.Lock()
	f.opens++
	n := f.opens
	fn := f.openFn
	f.mu.Unlock()
	c, err := fn(ctx, url, n)
	if c != nil {
		f.mu.Lock()
		f.clients = append(f.clients, c)
		f.mu.Unlock()
	}
	return c, err
}

func (f *fakeFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

type fakeClient struct {
	signInFn func(context.Context, string, string) error
	useFn    func(context.Context, string, string) error
	queryFn  func(context.Context, string, map[string]any) (any, error)
	closeFn  func(context.Context) error
	closes   atomic.Int32
	queries  atomic.Int32
}

func (c *fakeClient) SignIn(ctx context.Context, u, p string) error {
	if c.signInFn != nil {
		return c.signInFn(ctx, u, p)
	}
	return nil
}
func (c *fakeClient) Use(ctx context.Context, ns, db string) error {
	if c.useFn != nil {
		return c.useFn(ctx, ns, db)
	}
	return nil
}
func (c *fakeClient) Query(ctx context.Context, q string, v map[string]any) (any, error) {
	c.queries.Add(1)
	if c.queryFn != nil {
		return c.queryFn(ctx, q, v)
	}
	return []any{}, nil
}
func (c *fakeClient) Close(ctx context.Context) error {
	c.closes.Add(1)
	if c.closeFn != nil {
		return c.closeFn(ctx)
	}
	return nil
}

func testDriver(t *testing.T, f DBFactory) *SurrealDriver {
	t.Helper()
	cfg := DefaultDriverConfig()
	cfg.Username = "root"
	cfg.Password = "root"
	d, err := NewSurrealDriver(cfg, f)
	if err != nil {
		t.Fatal(err)
	}
	d.retryDelay = func(int) time.Duration { return 0 }
	d.sleep = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	return d
}

func TestConnectIsConcurrencySafeAndIdempotent(t *testing.T) {
	client := &fakeClient{}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) {
		time.Sleep(5 * time.Millisecond)
		return client, nil
	}}
	d := testDriver(t, f)
	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errCh <- d.Connect(context.Background()) }()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
	}
	if got := f.count(); got != 1 {
		t.Fatalf("factory opens=%d want 1", got)
	}
	if d.snapshot() == nil {
		t.Fatal("no current client")
	}
}

func TestConnectCleansPartiallyInitializedClient(t *testing.T) {
	client := &fakeClient{signInFn: func(context.Context, string, string) error { return errors.New("bad auth") }}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, f)
	err := d.Connect(context.Background())
	if err == nil || !errors.Is(err, ErrConnection) {
		t.Fatalf("err=%v", err)
	}
	if d.snapshot() != nil {
		t.Fatal("partial client was published")
	}
	if got := client.closes.Load(); got != 1 {
		t.Fatalf("closes=%d want 1", got)
	}
}

func TestConnectCancellationCleansPartialClient(t *testing.T) {
	client := &fakeClient{signInFn: func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := d.Connect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
	if d.snapshot() != nil {
		t.Fatal("cancelled client was published")
	}
	// Because the cancellation can be observed by the context-aware lock before
	// factory.Open runs, cleanup is required only if a partial client existed.
	if f.count() > 0 && client.closes.Load() != 1 {
		t.Fatalf("closes=%d", client.closes.Load())
	}
}

func TestCancellationAfterOpenCleansPartialClient(t *testing.T) {
	opened := make(chan struct{})
	client := &fakeClient{signInFn: func(ctx context.Context, _, _ string) error {
		close(opened)
		<-ctx.Done()
		return ctx.Err()
	}}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Connect(ctx) }()
	<-opened
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if client.closes.Load() != 1 {
		t.Fatalf("closes=%d want 1", client.closes.Load())
	}
}

func TestConcurrentStaleQueriesShareOneReconnect(t *testing.T) {
	const n = 12
	arrived := make(chan struct{}, n)
	release := make(chan struct{})
	old := &fakeClient{}
	old.queryFn = func(context.Context, string, map[string]any) (any, error) {
		arrived <- struct{}{}
		<-release
		return nil, errors.New("websocket connection closed")
	}
	fresh := &fakeClient{queryFn: func(context.Context, string, map[string]any) (any, error) { return "ok", nil }}
	f := &fakeFactory{openFn: func(_ context.Context, _ string, call int) (DBClient, error) {
		if call == 1 {
			return old, nil
		}
		if call == 2 {
			return fresh, nil
		}
		return nil, fmt.Errorf("unexpected open %d", call)
	}}
	d := testDriver(t, f)
	if err := d.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := d.Query(context.Background(), "SELECT 1", nil)
			if err == nil && got != "ok" {
				err = fmt.Errorf("got %v", got)
			}
			errCh <- err
		}()
	}
	for i := 0; i < n; i++ {
		<-arrived
	}
	close(release)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("query: %v", err)
		}
	}
	if got := f.count(); got != 2 {
		t.Fatalf("factory opens=%d want 2", got)
	}
	if got := old.closes.Load(); got != 1 {
		t.Fatalf("old closes=%d want 1", got)
	}
	if got := fresh.closes.Load(); got != 0 {
		t.Fatalf("fresh closes=%d want 0", got)
	}
}

func TestReconnectFailureRetriedWithinAttemptBudget(t *testing.T) {
	old := &fakeClient{queryFn: func(context.Context, string, map[string]any) (any, error) { return nil, errors.New("broken pipe") }}
	fresh := &fakeClient{queryFn: func(context.Context, string, map[string]any) (any, error) { return 42, nil }}
	f := &fakeFactory{openFn: func(_ context.Context, _ string, call int) (DBClient, error) {
		switch call {
		case 1:
			return old, nil
		case 2:
			return nil, errors.New("server rebooting")
		case 3:
			return fresh, nil
		default:
			return nil, fmt.Errorf("unexpected open %d", call)
		}
	}}
	d := testDriver(t, f)
	if err := d.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := d.Query(context.Background(), "SELECT 1", nil)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != 42 {
		t.Fatalf("got=%v", got)
	}
	if f.count() != 3 {
		t.Fatalf("opens=%d", f.count())
	}
}

func TestTransactionConflictsRetryWithoutReconnect(t *testing.T) {
	var calls atomic.Int32
	client := &fakeClient{queryFn: func(context.Context, string, map[string]any) (any, error) {
		n := calls.Add(1)
		if n < 3 {
			return nil, errors.New("transaction conflict: resource busy")
		}
		return "ok", nil
	}}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, f)
	if err := d.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := d.Query(context.Background(), "UPSERT x", nil)
	if err != nil || got != "ok" {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if f.count() != 1 {
		t.Fatalf("opens=%d want 1", f.count())
	}
	if client.closes.Load() != 0 {
		t.Fatalf("closes=%d want 0", client.closes.Load())
	}
	if calls.Load() != 3 {
		t.Fatalf("queries=%d", calls.Load())
	}
}

func TestNonTransientQueryErrorIsNotRetried(t *testing.T) {
	client := &fakeClient{queryFn: func(context.Context, string, map[string]any) (any, error) { return nil, errors.New("syntax error") }}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, f)
	if err := d.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := d.Query(context.Background(), "BAD", nil)
	if err == nil || err.Error() != "syntax error" {
		t.Fatalf("err=%v", err)
	}
	if client.queries.Load() != 1 {
		t.Fatalf("queries=%d", client.queries.Load())
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	client := &fakeClient{}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, f)
	if err := d.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if client.closes.Load() != 1 {
		t.Fatalf("closes=%d", client.closes.Load())
	}
}

func TestClosePreventsStaleRequestReopeningDriver(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	client := &fakeClient{queryFn: func(context.Context, string, map[string]any) (any, error) {
		close(started)
		<-release
		return nil, errors.New("connection closed")
	}}
	f := &fakeFactory{openFn: func(context.Context, string, int) (DBClient, error) { return client, nil }}
	d := testDriver(t, f)
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := d.Query(ctx, "RETURN 1", nil); done <- err }()
	<-started
	if err := d.Close(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("stale query succeeded after close")
	}
	if f.count() != 1 || d.snapshot() != nil {
		t.Fatal("stale query reopened closed driver")
	}
	if err := d.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count() != 2 {
		t.Fatal("explicit reconnect should work")
	}
	_ = d.Close(ctx)
}
