package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
)

type interactionStore struct {
	*fakeStore
	stamped []domain.SessionID
}

func (s *interactionStore) RecordSessionInteraction(_ context.Context, id domain.SessionID, _ string, _ time.Time) error {
	s.stamped = append(s.stamped, id)
	return nil
}

func TestTriggerRequestedRecordsAnInteractionThatEndsAnOrchestratorWait(t *testing.T) {
	st := &interactionStore{fakeStore: &fakeStore{}}
	svc := New(nil, st)
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{Created: true}, nil
	}
	if _, err := svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{Source: domain.ReviewTriggerAgent}); err != nil {
		t.Fatal(err)
	}
	if len(st.stamped) != 1 || st.stamped[0] != "worker-1" {
		t.Fatalf("stamped %v, want the reviewed worker once", st.stamped)
	}
}
