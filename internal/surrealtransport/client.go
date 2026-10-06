// Package surrealtransport adapts the SurrealDB SDK and normalizes its wire values.
package surrealtransport

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	surrealdb "github.com/surrealdb/surrealdb.go"
	"github.com/surrealdb/surrealdb.go/pkg/models"
)

// Open connects to an endpoint without authenticating or selecting a database.
func Open(ctx context.Context, url string) (*Client, error) {
	db, err := surrealdb.FromEndpointURLString(ctx, url)
	if err != nil {
		return nil, err
	}
	return &Client{db: db}, nil
}

type Client struct {
	db *surrealdb.DB
}

func (c *Client) SignIn(ctx context.Context, username, password string) error {
	_, err := c.db.SignIn(ctx, map[string]any{
		"user": username,
		"pass": password,
	})
	return err
}

func (c *Client) Use(ctx context.Context, namespace, database string) error {
	return c.db.Use(ctx, namespace, database)
}

func (c *Client) Query(ctx context.Context, surql string, variables map[string]any) (any, error) {
	results, err := surrealdb.Query[any](ctx, c.db, surql, normalizeSurrealVariables(variables))
	if err != nil {
		return nil, err
	}
	if results == nil || len(*results) == 0 {
		return []any{}, nil
	}
	return normalizeSurrealResult((*results)[len(*results)-1].Result), nil
}

func (c *Client) Close(ctx context.Context) error {
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
