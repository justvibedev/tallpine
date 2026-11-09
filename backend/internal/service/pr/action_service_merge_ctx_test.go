package pr

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type ctxActionLifecycle struct{ requester domain.SessionID }

func (f *ctxActionLifecycle) NoteMergeRequester(domain.SessionID, domain.SessionID) {}

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

type noteOrderLifecycle struct {
	ctxActionLifecycle
	events *[]string
}

func (f *noteOrderLifecycle) NoteMergeRequester(id, requester domain.SessionID) {
	*f.events = append(*f.events, "note:"+string(id)+":"+string(requester))
}

type orderWriter struct {
	fakeActionWriter
	events *[]string
}

func (w *orderWriter) MarkPRMerged(ctx context.Context, url, sha string, at time.Time) error {
	*w.events = append(*w.events, "persist")
	return w.fakeActionWriter.MarkPRMerged(ctx, url, sha, at)
}

// B609: the requester is noted before the merge is persisted, so an idle
// signal between the two still attributes the close to the merge.
func TestActionServiceMerge_NotesRequesterBeforePersisting(t *testing.T) {
	pr, scm := mergeableActionFixture()
	pr.SessionID = "session-1"
	var events []string
	svc := NewActionService(ActionDeps{
		Store:       &fakeActionStore{pr: pr, ok: true},
		Reader:      scm,
		Merger:      scm,
		MergeWriter: &orderWriter{events: &events},
		Lifecycle:   &noteOrderLifecycle{events: &events},
		Clock:       time.Now,
	})
	if _, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA, Requester: "orch-1"}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != "note:session-1:orch-1" || events[1] != "persist" {
		t.Fatalf("events = %v, want note before persist", events)
	}
}

func (f *fakeActionLifecycle) NoteMergeRequester(domain.SessionID, domain.SessionID) {}
