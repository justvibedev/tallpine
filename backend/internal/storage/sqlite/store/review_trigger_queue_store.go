package store

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// EnqueueReviewTrigger records a pass waiting for its reviewer. Queuing the
// same PR for the same reviewer twice keeps the first entry.
func (s *Store) EnqueueReviewTrigger(ctx context.Context, q domain.QueuedReviewTrigger) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if q.TriggerSource == "" {
		q.TriggerSource = domain.ReviewTriggerAgent
	}
	next := q.NextAttemptAt
	if next.IsZero() {
		next = q.CreatedAt
	}
	return s.qw.EnqueueReviewTrigger(ctx, gen.EnqueueReviewTriggerParams{
		SessionID: string(q.SessionID), PRURL: q.PRURL, Harness: string(q.Harness), CreatedAt: q.CreatedAt,
		Model: q.Model, Effort: q.Effort, InterfaceMode: string(q.InterfaceMode), TriggerSource: string(q.TriggerSource), NextAttemptAt: next,
	})
}

// ListQueuedReviewTriggers returns every waiting pass, oldest first.
func (s *Store) ListQueuedReviewTriggers(ctx context.Context) ([]domain.QueuedReviewTrigger, error) {
	rows, err := s.qr.ListQueuedReviewTriggers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list queued review triggers: %w", err)
	}
	out := make([]domain.QueuedReviewTrigger, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.QueuedReviewTrigger{
			SessionID: domain.SessionID(row.SessionID), PRURL: row.PRURL, Harness: domain.ReviewerHarness(row.Harness), CreatedAt: row.CreatedAt,
			Model: row.Model, Effort: row.Effort, InterfaceMode: domain.ReviewerInterfaceMode(row.InterfaceMode), TriggerSource: domain.ReviewTriggerSource(row.TriggerSource),
			Attempts: int(row.Attempts), NextAttemptAt: row.NextAttemptAt,
		})
	}
	return out, nil
}

// RecordQueuedReviewTriggerAttempt counts a failed start and delays the next one.
func (s *Store) RecordQueuedReviewTriggerAttempt(ctx context.Context, q domain.QueuedReviewTrigger, nextAttemptAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.RecordQueuedReviewTriggerAttempt(ctx, gen.RecordQueuedReviewTriggerAttemptParams{
		NextAttemptAt: nextAttemptAt, SessionID: string(q.SessionID), PRURL: q.PRURL, Harness: string(q.Harness),
	})
}

// DeleteQueuedReviewTrigger removes a waiting pass once it started or can no
// longer start.
func (s *Store) DeleteQueuedReviewTrigger(ctx context.Context, q domain.QueuedReviewTrigger) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.DeleteQueuedReviewTrigger(ctx, gen.DeleteQueuedReviewTriggerParams{SessionID: string(q.SessionID), PRURL: q.PRURL, Harness: string(q.Harness)})
}
