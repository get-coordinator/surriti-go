package surriti

import (
	"context"

	"github.com/get-coordinator/surriti-go/internal/surrealtransport"
)

// OfficialSurrealFactory adapts the pinned official SurrealDB Go SDK to DBFactory.
type OfficialSurrealFactory struct{}

func (OfficialSurrealFactory) Open(ctx context.Context, url string) (DBClient, error) {
	client, err := surrealtransport.Open(ctx, url)
	if err != nil {
		return nil, err
	}
	return client, nil
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
