package store

import (
	"context"

	"github.com/google/uuid"
)

// NoopStore is a Store implementation that does nothing.
// Used when ArangoDB persistence is disabled.
type NoopStore struct{}

func (n *NoopStore) CreateJob(_ context.Context, _ string) (string, error) {
	return uuid.New().String(), nil
}

func (n *NoopStore) SaveTier1(_ context.Context, _ string, _ *Tier1Result) error { return nil }
func (n *NoopStore) SaveTier2(_ context.Context, _ string, _ *Tier2Result) error { return nil }
func (n *NoopStore) SaveTier3(_ context.Context, _ string, _ *Tier3Result) error { return nil }

func (n *NoopStore) GetJob(_ context.Context, _ string) (*ExtractionJob, error) {
	return nil, ErrJobNotFound
}
