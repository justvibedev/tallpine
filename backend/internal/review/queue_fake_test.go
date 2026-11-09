package review

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// queueStore adds an in-memory trigger queue to fakeStore.
type queueStore struct {
	*fakeStore
	queued []domain.QueuedReviewTrigger
}

func (f *queueStore) EnqueueReviewTrigger(_ context.Context, q domain.QueuedReviewTrigger) error {
	for _, existing := range f.queued {
		if existing.SessionID == q.SessionID && existing.PRURL == q.PRURL && existing.Harness == q.Harness {
			return nil
		}
	}
	f.queued = append(f.queued, q)
	return nil
}

func (f *queueStore) ListQueuedReviewTriggers(context.Context) ([]domain.QueuedReviewTrigger, error) {
	return append([]domain.QueuedReviewTrigger(nil), f.queued...), nil
}

func (f *queueStore) DeleteQueuedReviewTrigger(_ context.Context, q domain.QueuedReviewTrigger) error {
	kept := f.queued[:0]
	for _, existing := range f.queued {
		if existing.SessionID != q.SessionID || existing.PRURL != q.PRURL || existing.Harness != q.Harness {
			kept = append(kept, existing)
		}
	}
	f.queued = kept
	return nil
}

func (f *queueStore) RecordQueuedReviewTriggerAttempt(_ context.Context, q domain.QueuedReviewTrigger, next time.Time) error {
	for i := range f.queued {
		if f.queued[i].SessionID == q.SessionID && f.queued[i].PRURL == q.PRURL && f.queued[i].Harness == q.Harness {
			f.queued[i].Attempts++
			f.queued[i].NextAttemptAt = next
		}
	}
	return nil
}
