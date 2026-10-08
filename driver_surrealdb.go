package surriti

import (
	"context"
	"time"

	"github.com/get-coordinator/surriti-go/internal/surrealtransport"
)

// OfficialSurrealFactory adapts the pinned official SurrealDB Go SDK to DBFactory.
// QueryTimeout bounds each request; zero leaves it to the caller's context.
type OfficialSurrealFactory struct {
	QueryTimeout time.Duration
}

func (f OfficialSurrealFactory) Open(ctx context.Context, url string) (DBClient, error) {
	client, err := surrealtransport.OpenTimeout(ctx, url, f.QueryTimeout)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func NewDefaultSurrealDriver(cfg DriverConfig) (*SurrealDriver, error) {
	return NewSurrealDriver(cfg, OfficialSurrealFactory{QueryTimeout: cfg.QueryTimeout})
}

func NewSurrealDriverFromEnv() (*SurrealDriver, error) {
	cfg, err := DriverConfigFromEnv()
	if err != nil {
		return nil, err
	}
	return NewDefaultSurrealDriver(cfg)
}
