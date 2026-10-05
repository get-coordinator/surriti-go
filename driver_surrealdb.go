package surriti

import (
	"context"

	surrealdb "github.com/surrealdb/surrealdb.go"
)

// OfficialSurrealFactory adapts the pinned official SurrealDB Go SDK to the
// transport-neutral DBFactory used by the reliability state machine.
type OfficialSurrealFactory struct{}

func (OfficialSurrealFactory) Open(ctx context.Context, url string) (DBClient, error) {
	db, err := surrealdb.FromEndpointURLString(ctx, url)
	if err != nil {
		return nil, err
	}
	return &officialSurrealClient{db: db}, nil
}

type officialSurrealClient struct {
	db *surrealdb.DB
}

func (c *officialSurrealClient) SignIn(ctx context.Context, username, password string) error {
	_, err := c.db.SignIn(ctx, map[string]any{
		"user": username,
		"pass": password,
	})
	return err
}

func (c *officialSurrealClient) Use(ctx context.Context, namespace, database string) error {
	return c.db.Use(ctx, namespace, database)
}

func (c *officialSurrealClient) Query(ctx context.Context, surql string, variables map[string]any) (any, error) {
	results, err := surrealdb.Query[any](ctx, c.db, surql, variables)
	if err != nil {
		return nil, err
	}
	if results == nil || len(*results) == 0 {
		return []any{}, nil
	}
	return (*results)[len(*results)-1].Result, nil
}

func (c *officialSurrealClient) Close(ctx context.Context) error {
	return c.db.Close(ctx)
}

func NewDefaultSurrealDriver(cfg DriverConfig) (*SurrealDriver, error) {
	return NewSurrealDriver(cfg, OfficialSurrealFactory{})
}

func NewSurrealDriverFromEnv() (*SurrealDriver, error) {
	cfg, err := DriverConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return NewDefaultSurrealDriver(cfg)
}
