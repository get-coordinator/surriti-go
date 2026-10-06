package surriti

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var staleConnectionTokens = []string{
	"no close frame",
	"connectionclosed",
	"connection closed",
	"websocket",
	"not connected",
	"broken pipe",
	"connection reset",
}

var transactionConflictTokens = []string{
	"transaction conflict",
	"write conflict",
	"resource busy",
}

// DBClient is the deliberately tiny transport contract used by SurrealDriver.
// The official SurrealDB Go SDK is adapted to this interface in the transport
// adapter; tests use deterministic fakes. Core memory logic never imports a
// vendor SDK directly.
type DBClient interface {
	SignIn(ctx context.Context, username, password string) error
	Use(ctx context.Context, namespace, database string) error
	Query(ctx context.Context, surql string, variables map[string]any) (any, error)
	Close(ctx context.Context) error
}

// DBFactory creates an already-connected low-level client for a SurrealDB
// endpoint. Authentication and namespace/database selection are intentionally
// performed by SurrealDriver so reconnects replay the same lifecycle.
type DBFactory interface {
	Open(ctx context.Context, url string) (DBClient, error)
}

// Queryer is the storage interface used by the rest of Surriti. It is small on
// purpose so deterministic/in-memory implementations can replace SurrealDB in
// unit tests without changing memory behavior.
type Queryer interface {
	Query(ctx context.Context, surql string, variables map[string]any) (any, error)
}

// DriverConfig mirrors the Python SurrealDriver constructor and environment
// variables. MaxQueryAttempts is operational rather than persistent state.
type DriverConfig struct {
	URL              string
	Namespace        string
	Database         string
	Username         string
	Password         string
	EmbeddingDim     int
	MaxQueryAttempts int
}

func DefaultDriverConfig() DriverConfig {
	return DriverConfig{
		URL:              "ws://localhost:8000/rpc",
		Namespace:        "surriti",
		Database:         "surriti",
		EmbeddingDim:     768,
		MaxQueryAttempts: 3,
	}
}

func DriverConfigFromEnv() (DriverConfig, error) {
	cfg := DefaultDriverConfig()
	if v := os.Getenv("SURRITI_SURREAL_URL"); v != "" {
		cfg.URL = v
	}
	if v := os.Getenv("SURRITI_SURREAL_NS"); v != "" {
		cfg.Namespace = v
	}
	if v := os.Getenv("SURRITI_SURREAL_DB"); v != "" {
		cfg.Database = v
	}
	cfg.Username = os.Getenv("SURRITI_SURREAL_USER")
	cfg.Password = os.Getenv("SURRITI_SURREAL_PASS")
	if v := os.Getenv("SURRITI_EMBEDDING_DIM"); v != "" {
		dim, err := strconv.Atoi(v)
		if err != nil {
			return DriverConfig{}, fmt.Errorf("%w: SURRITI_EMBEDDING_DIM=%q is not an integer: %v", ErrConfig, v, err)
		}
		cfg.EmbeddingDim = dim
	}
	if err := cfg.validate(); err != nil {
		return DriverConfig{}, err
	}
	return cfg, nil
}

func (c DriverConfig) validate() error {
	if c.EmbeddingDim <= 0 {
		return fmt.Errorf("%w: embedding_dim must be a positive integer, got %d", ErrConfig, c.EmbeddingDim)
	}
	if c.MaxQueryAttempts <= 0 {
		return fmt.Errorf("%w: max query attempts must be positive, got %d", ErrConfig, c.MaxQueryAttempts)
	}
	return nil
}

// contextMutex is a context-aware binary mutex. sync.Mutex is intentionally
// not used for connection lifecycle locks because a request waiting behind a
// slow connect/reconnect must still be cancellable.
type contextMutex struct{ token chan struct{} }

func newContextMutex() *contextMutex {
	m := &contextMutex{token: make(chan struct{}, 1)}
	m.token <- struct{}{}
	return m
}

func (m *contextMutex) Lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		return nil
	}
}

func (m *contextMutex) Unlock() { m.token <- struct{}{} }

type clientHandle struct {
	client DBClient
}

// SurrealDriver owns a single authenticated, selected SurrealDB connection
// and reproduces the Python driver's bounded retry/reconnect behavior.
type SurrealDriver struct {
	cfg     DriverConfig
	factory DBFactory

	stateMu sync.RWMutex
	current *clientHandle
	// Protected by connectMu. Only an explicit Connect may reopen after Close.
	closed bool

	connectMu   *contextMutex
	reconnectMu *contextMutex

	// Test seams. Production constructors install the default implementations.
	retryDelay func(attempt int) time.Duration
	sleep      func(context.Context, time.Duration) error
}

func NewSurrealDriver(cfg DriverConfig, factory DBFactory) (*SurrealDriver, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, fmt.Errorf("%w: nil database factory", ErrConfig)
	}
	return &SurrealDriver{
		cfg:         cfg,
		factory:     factory,
		connectMu:   newContextMutex(),
		reconnectMu: newContextMutex(),
		retryDelay:  defaultRetryDelay,
		sleep:       sleepContext,
	}, nil
}

func (d *SurrealDriver) Config() DriverConfig { return d.cfg }

func defaultRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := 200 * time.Millisecond
	for i := 0; i < attempt; i++ {
		base *= 2
		if base >= 2*time.Second {
			base = 2 * time.Second
			break
		}
	}
	// Match the Python policy: base + uniform(0, base*0.25).
	jitter := time.Duration(rand.Float64() * 0.25 * float64(base))
	return base + jitter
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (d *SurrealDriver) snapshot() *clientHandle {
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	return d.current
}

func (d *SurrealDriver) setCurrent(h *clientHandle) {
	d.stateMu.Lock()
	d.current = h
	d.stateMu.Unlock()
}

func (d *SurrealDriver) nextHandle(client DBClient) *clientHandle {
	d.stateMu.Lock()
	h := &clientHandle{client: client}
	d.current = h
	d.stateMu.Unlock()
	return h
}

// Connect is idempotent and concurrency-safe. Exactly one successfully
// authenticated client becomes current even when many goroutines race to
// connect.
func (d *SurrealDriver) Connect(ctx context.Context) error {
	if err := d.connectMu.Lock(ctx); err != nil {
		return err
	}
	defer d.connectMu.Unlock()
	if d.snapshot() != nil {
		return nil
	}
	d.closed = false
	_, err := d.connectLocked(ctx)
	return err
}

// connectLocked requires connectMu. It never publishes a partially initialized
// client. Any failure or cancellation closes the candidate connection using an
// independent cleanup context.
func (d *SurrealDriver) connectLocked(ctx context.Context) (*clientHandle, error) {
	client, err := d.factory.Open(ctx, d.cfg.URL)
	if err != nil {
		cleanupClose(client)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: could not connect to SurrealDB at %q: %v", ErrConnection, d.cfg.URL, err)
	}
	published := false
	defer func() {
		if !published {
			cleanupClose(client)
		}
	}()

	// Python signs in only when both fields are present. Preserve that behavior.
	if d.cfg.Username != "" && d.cfg.Password != "" {
		if err := client.SignIn(ctx, d.cfg.Username, d.cfg.Password); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, fmt.Errorf("%w: could not authenticate to SurrealDB at %q: %v", ErrConnection, d.cfg.URL, err)
		}
	}
	if err := client.Use(ctx, d.cfg.Namespace, d.cfg.Database); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: could not select SurrealDB namespace/database %q/%q: %v", ErrConnection, d.cfg.Namespace, d.cfg.Database, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h := d.nextHandle(client)
	published = true
	return h, nil
}

func cleanupClose(client DBClient) {
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}

// Close is idempotent. Like the Python reference implementation, transport
// close failures are best-effort cleanup and do not escape to callers. The
// current pointer is cleared before invoking the potentially slow close.
func (d *SurrealDriver) Close(ctx context.Context) error {
	if err := d.connectMu.Lock(ctx); err != nil {
		return err
	}
	defer d.connectMu.Unlock()

	d.closed = true
	d.stateMu.Lock()
	h := d.current
	d.current = nil
	d.stateMu.Unlock()
	if h == nil {
		return nil
	}
	_ = h.client.Close(ctx)
	return nil
}

func isTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, token := range transactionConflictTokens {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

func isStaleConnection(err error) bool {
	if err == nil {
		return false
	}
	// Keep classification byte-for-byte compatible with the Python policy.
	// Broader transport heuristics can be added after parity certification.
	msg := strings.ToLower(err.Error())
	for _, token := range staleConnectionTokens {
		if strings.Contains(msg, token) {
			return true
		}
	}
	return false
}

// Query mirrors the Python driver's retry semantics:
//   - query/logic errors are returned immediately;
//   - transaction conflicts retry on the same healthy client;
//   - stale connections trigger one shared reconnect under contention;
//   - attempts are bounded by MaxQueryAttempts.
func (d *SurrealDriver) Query(ctx context.Context, surql string, variables map[string]any) (any, error) {
	if variables == nil {
		variables = map[string]any{}
	}
	var reconnectRequired bool
	var failed *clientHandle
	var lastConnectionErr error

	for attempt := 0; attempt < d.cfg.MaxQueryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if reconnectRequired {
			if err := d.sleep(ctx, d.retryDelay(attempt-1)); err != nil {
				return nil, err
			}
			if err := d.reconnectIfCurrent(ctx, failed); err != nil {
				lastConnectionErr = err
				if attempt == d.cfg.MaxQueryAttempts-1 {
					return nil, fmt.Errorf("%w: SurrealDB connection to %q could not be re-established after %d attempts: %v", ErrConnection, d.cfg.URL, d.cfg.MaxQueryAttempts, err)
				}
				continue
			}
			reconnectRequired = false
		}

		h := d.snapshot()
		if h == nil {
			return nil, fmt.Errorf("%w: SurrealDriver is not connected; call Connect before Query", ErrConnection)
		}

		result, err := h.client.Query(ctx, surql, variables)
		if err == nil {
			return result, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if isTransactionConflict(err) {
			if attempt == d.cfg.MaxQueryAttempts-1 {
				return nil, err
			}
			if err := d.sleep(ctx, d.retryDelay(attempt)); err != nil {
				return nil, err
			}
			continue
		}
		if !isStaleConnection(err) {
			return nil, err
		}
		if attempt == d.cfg.MaxQueryAttempts-1 {
			return nil, fmt.Errorf("%w: SurrealDB connection to %q could not be re-established after %d attempts: %v", ErrConnection, d.cfg.URL, d.cfg.MaxQueryAttempts, err)
		}
		failed = h
		lastConnectionErr = err
		reconnectRequired = true
	}

	return nil, fmt.Errorf("%w: SurrealDB connection to %q could not be re-established after %d attempts: %v", ErrConnection, d.cfg.URL, d.cfg.MaxQueryAttempts, lastConnectionErr)
}

// reconnectIfCurrent serializes reconnects and uses pointer identity to avoid
// an old failing goroutine closing a newer healthy client installed by another
// goroutine.
func (d *SurrealDriver) reconnectIfCurrent(ctx context.Context, failed *clientHandle) error {
	if err := d.reconnectMu.Lock(ctx); err != nil {
		return err
	}
	defer d.reconnectMu.Unlock()

	current := d.snapshot()
	if current != nil && failed != nil && current != failed {
		// Another goroutine already replaced the failed connection.
		return nil
	}
	if current != nil && failed == nil {
		return nil
	}

	if err := d.connectMu.Lock(ctx); err != nil {
		return err
	}
	defer d.connectMu.Unlock()

	if d.closed {
		return fmt.Errorf("%w: driver closed during reconnect", ErrConnection)
	}
	// Re-check after obtaining the connect lock.
	current = d.snapshot()
	if current != nil && failed != nil && current != failed {
		return nil
	}
	if current != nil && failed == nil {
		return nil
	}

	if current != nil {
		d.setCurrent(nil)
		cleanupClose(current.client)
	}
	_, err := d.connectLocked(ctx)
	return err
}

var AllTables = []string{
	"mentions",
	"relates_to",
	"memory_ref",
	"has_member",
	"episode",
	"entity",
	"entity_alias",
	"resource",
	"community",
	"relation_frame",
}

// Clear reproduces the Python destructive-operation guard and table order.
func (d *SurrealDriver) Clear(ctx context.Context) error {
	allow := strings.ToLower(strings.TrimSpace(os.Getenv("SURRITI_ALLOW_DESTRUCTIVE")))
	if allow != "1" && allow != "true" && allow != "yes" {
		return fmt.Errorf("surriti: SurrealDriver.Clear is disabled; set SURRITI_ALLOW_DESTRUCTIVE=1 to allow destructive operations")
	}
	for _, table := range AllTables {
		var last error
		for attempt := 0; attempt < 3; attempt++ {
			_, err := d.Query(ctx, "DELETE "+table+";", nil)
			if err == nil {
				last = nil
				break
			}
			last = err
			if !isTransactionConflict(err) {
				return err
			}
			if err := d.sleep(ctx, 200*time.Millisecond); err != nil {
				return err
			}
		}
		if last != nil {
			return last
		}
	}
	return nil
}
