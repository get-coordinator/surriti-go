// Package surrealtransport adapts the SurrealDB SDK and normalizes its wire values.
package surrealtransport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/connection"
	"github.com/surrealdb/surrealdb.go/pkg/connection/gorillaws"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Open connects to an endpoint without authenticating or selecting a database.
func Open(ctx context.Context, endpoint string) (*Client, error) {
	u, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return nil, err
	}
	lifetime, disconnected := context.WithCancel(context.Background())
	client := &Client{lifetime: lifetime, disconnected: disconnected}
	if u.Scheme == "ws" || u.Scheme == "wss" {
		cfg := connection.NewConfig(u)
		if err := cfg.Validate(); err != nil {
			disconnected()
			return nil, err
		}
		ws := gorillaws.New(cfg)
		var socket *websocket.Conn
		client.lastPong.Store(time.Now().UnixNano())
		// SDK v1.7 waits for an explicit Close after a peer close. Notify
		// callers here so idle and in-flight requests can drive reconnect.
		ws.Option = append(ws.Option, func(conn *gorillaws.Connection) error {
			socket = conn.Conn
			conn.Conn.SetPongHandler(func(string) error { client.lastPong.Store(time.Now().UnixNano()); return nil })
			conn.Conn.SetCloseHandler(func(code int, text string) error { disconnected(); return nil })
			return nil
		})
		client.db, err = surrealdb.FromConnection(ctx, ws)
		if err == nil {
			go client.watchSocket(socket)
		}
	} else {
		client.db, err = surrealdb.FromEndpointURLString(ctx, endpoint)
	}
	if err != nil {
		disconnected()
		return nil, err
	}
	return client, nil
}

type Client struct {
	db           *surrealdb.DB
	lifetime     context.Context
	disconnected context.CancelFunc
	lastPong     atomic.Int64
}

// A TCP disconnect need not carry a WebSocket close frame. Heartbeats also
// bound that path, which the SDK read loop otherwise leaves waiting on Close.
func (c *Client) watchSocket(socket *websocket.Conn) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.lifetime.Done():
			return
		case now := <-ticker.C:
			if now.Sub(time.Unix(0, c.lastPong.Load())) > 15*time.Second {
				c.disconnected()
				return
			}
			if err := socket.WriteControl(websocket.PingMessage, nil, now.Add(2*time.Second)); err != nil {
				c.disconnected()
				return
			}
		}
	}
}

func (c *Client) callContext(ctx context.Context) (context.Context, func()) {
	call, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.lifetime, cancel)
	return call, func() { stop(); cancel() }
}
func (c *Client) transportError(err error) error {
	if err != nil && c.lifetime.Err() != nil {
		return fmt.Errorf("websocket connection closed: %w", err)
	}
	return err
}

func (c *Client) SignIn(ctx context.Context, username, password string) error {
	if c.lifetime.Err() != nil {
		return fmt.Errorf("websocket connection closed")
	}
	call, done := c.callContext(ctx)
	defer done()
	_, err := c.db.SignIn(call, map[string]any{
		"user": username,
		"pass": password,
	})
	return c.transportError(err)
}

func (c *Client) Use(ctx context.Context, namespace, database string) error {
	if c.lifetime.Err() != nil {
		return fmt.Errorf("websocket connection closed")
	}
	call, done := c.callContext(ctx)
	defer done()
	return c.transportError(c.db.Use(call, namespace, database))
}

func (c *Client) Query(ctx context.Context, surql string, variables map[string]any) (any, error) {
	if c.lifetime.Err() != nil {
		return nil, fmt.Errorf("websocket connection closed")
	}
	call, done := c.callContext(ctx)
	defer done()
	results, err := surrealdb.Query[any](call, c.db, surql, normalizeSurrealVariables(variables))
	if err != nil {
		return nil, c.transportError(err)
	}
	if results == nil || len(*results) == 0 {
		return []any{}, nil
	}
	return normalizeSurrealResult((*results)[len(*results)-1].Result), nil
}

func (c *Client) Close(ctx context.Context) error {
	c.disconnected()
	return c.db.Close(ctx)
}

func normalizeSurrealVariables(variables map[string]any) map[string]any {
	if variables == nil {
		return nil
	}
	out := make(map[string]any, len(variables))
	for k, v := range variables {
		out[k] = normalizeSurrealValue(v)
	}
	return out
}

// Python's SurrealDB client encodes None as SurrealDB NONE. Go's plain nil is
// CBOR null, which is observably different for option<T> schema fields.
// Normalize query variables recursively so the Go port preserves Python's wire
// semantics without weakening schema types.
func normalizeSurrealValue(v any) any {
	if v == nil {
		return models.None
	}
	if n, ok := v.(json.Number); ok {
		if i, err := n.Int64(); err == nil {
			return i
		}
		if f, err := n.Float64(); err == nil {
			return f
		}
	}
	switch x := v.(type) {
	case time.Time:
		return &models.CustomDateTime{Time: x}
	case models.CustomDateTime:
		return &x
	case models.CustomNil:
		return v
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return models.None
		}
		return normalizeSurrealValue(rv.Elem().Interface())
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = normalizeSurrealValue(rv.Index(i).Interface())
		}
		return out
	case reflect.Map:
		if rv.Type().Key().Kind() == reflect.String {
			out := make(map[string]any, rv.Len())
			it := rv.MapRange()
			for it.Next() {
				out[it.Key().String()] = normalizeSurrealValue(it.Value().Interface())
			}
			return out
		}
	}
	return v
}

// Keep transport-specific datetimes/NONE at the adapter boundary. A plain
// time.Time CBOR encoding truncates fractional seconds with the SDK defaults;
// the Surreal tag preserves the timestamp and decodes back to Go time.Time.
func normalizeSurrealResult(v any) any {
	switch x := v.(type) {
	case models.CustomDateTime:
		return x.Time
	case *models.CustomDateTime:
		if x != nil {
			return x.Time
		}
		return nil
	case models.CustomNil:
		return nil
	case []any:
		for i := range x {
			x[i] = normalizeSurrealResult(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = normalizeSurrealResult(x[k])
		}
		return x
	default:
		return v
	}
}
