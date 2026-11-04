package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BacklogItemUsage is what an item's linked sessions have used, read from the
// usage ledger.
type BacklogItemUsage struct {
	// PricedNanos is the summed estimated cost in billionths of a USD.
	PricedNanos  int64
	Events       int64
	PricedEvents int64
	// FirstStarted is when the earliest linked session was created; zero when none.
	FirstStarted time.Time
}

// BacklogItemUsageFor sums the usage of every session linked to the item.
func (s *Store) BacklogItemUsageFor(ctx context.Context, itemID string) (BacklogItemUsage, error) {
	cost, err := s.qr.SelectBacklogItemUsageCost(ctx, itemID)
	if err != nil {
		return BacklogItemUsage{}, fmt.Errorf("select backlog item usage: %w", err)
	}
	out := BacklogItemUsage{PricedNanos: cost.PricedNanos, Events: cost.Events, PricedEvents: cost.PricedEvents}
	first, err := s.qr.SelectBacklogItemFirstSession(ctx, itemID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return BacklogItemUsage{}, fmt.Errorf("select backlog item sessions: %w", err)
	}
	if err == nil {
		out.FirstStarted = first
	}
	return out, nil
}

// BacklogRunUsageFor sums the usage of every distinct worker session linked to
// the run's items, so a session shared by two items is counted once.
func (s *Store) BacklogRunUsageFor(ctx context.Context, runID string) (BacklogItemUsage, error) {
	cost, err := s.qr.SelectBacklogRunUsageCost(ctx, runID)
	if err != nil {
		return BacklogItemUsage{}, fmt.Errorf("select backlog run usage: %w", err)
	}
	return BacklogItemUsage{PricedNanos: cost.PricedNanos, Events: cost.Events, PricedEvents: cost.PricedEvents}, nil
}
