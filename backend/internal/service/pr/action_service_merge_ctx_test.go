package pr

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type ctxActionLifecycle struct{ requester domain.SessionID }

func (f *ctxActionLifecycle) ApplySCMObservation(ctx context.Context, _ domain.SessionID, _ ports.SCMObservation) error {
	f.requester = ports.MergeRequester(ctx)
	return nil
}

// B560: the lifecycle reaction to a merge request runs on a merge-requested
// context, so end-on-merge auto-close sends the requester no notice.
func TestActionServiceMerge_PassesRequesterToLifecycle(t *testing.T) {
	pr, scm := mergeableActionFixture()
	pr.SessionID = "session-1"
	lifecycle := &ctxActionLifecycle{}
	svc := NewActionService(ActionDeps{
		Store:       &fakeActionStore{pr: pr, ok: true},
		Reader:      scm,
		Merger:      scm,
		MergeWriter: &fakeActionWriter{},
		Lifecycle:   lifecycle,
		Clock:       time.Now,
	})
	if _, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA, Requester: "orch-1"}); err != nil {
		t.Fatal(err)
	}
	if lifecycle.requester != "orch-1" {
		t.Fatalf("lifecycle context requester = %q, want orch-1", lifecycle.requester)
	}
}
