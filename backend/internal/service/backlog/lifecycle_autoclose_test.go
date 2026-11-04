package backlog_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// logged returns a lifecycle env whose service logs to the returned buffer.
func loggedEnv(t *testing.T) (*lifecycleEnv, *bytes.Buffer) {
	t.Helper()
	e := newLifecycleEnv(t)
	var buf bytes.Buffer
	e.svc = backlog.New(backlog.Deps{Store: e.store, Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	e.sub = backlog.NewSubscriber(e.svc, backlog.SubscriberConfig{})
	return e, &buf
}

// writePRAt writes a PR the provider reports as created at createdAt.
func (e *lifecycleEnv) writePRAt(sess domain.SessionID, number int, state string, createdAt time.Time) {
	e.t.Helper()
	pr := domain.PullRequest{
		URL: fmt.Sprintf("https://github.com/o/r/pull/%d", number), SessionID: sess, Number: number,
		UpdatedAt: time.Now().UTC(), Merged: state == "merged", Closed: state == "closed", CreatedAtProvider: createdAt,
	}
	if err := e.store.WriteSCMObservation(e.ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		e.t.Fatal(err)
	}
}

// B24: the session is already gone when the merge is recorded; the item still
// closes, because the facts come from the pr row, not the session's liveness.
func TestMergeRecordedAfterWorkerEndedStillClosesItem(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("ship it", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 7, "open")
	e.pump()
	e.endSession(sess)
	e.pump()
	e.writePR(sess.ID, 7, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone || got.ClosedReason != "merged #7" {
		t.Fatalf("got %s %q", got.State, got.ClosedReason)
	}
}

// B24: the observer must be told which ended sessions still have an open PR a
// live backlog item waits on, or it never polls them and the merge is missed.
func TestStoreListsEveryEndedSessionWithAnOpenPR(t *testing.T) {
	e := newLifecycleEnv(t)
	ended := func() []string {
		recs, err := e.store.ListEndedSessionsWithOpenBacklogPRs(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range recs {
			ids = append(ids, string(r.ID))
		}
		return ids
	}
	sess := e.session()
	it := e.item("waits", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 8, "open")
	if got := ended(); len(got) != 0 {
		t.Fatalf("live session listed: %v", got)
	}
	e.endSession(sess)
	if got := ended(); len(got) != 1 || got[0] != string(sess.ID) {
		t.Fatalf("ended session with open PR not listed: %v", got)
	}
	unlinked := e.session()
	e.writePR(unlinked.ID, 9, "open")
	e.endSession(unlinked)
	// B397: a session with no live backlog item is refreshed too, or its
	// stale 'open' PR would sit in the merge digest forever.
	if got := ended(); len(got) != 2 {
		t.Fatalf("ended session without a live backlog item not listed: %v", got)
	}
	e.writePR(sess.ID, 8, "merged")
	e.writePR(unlinked.ID, 9, "closed")
	if got := ended(); len(got) != 0 {
		t.Fatalf("merged PR still listed: %v", got)
	}
}

// B28: a reused worker links a new item; the PRs it opened before the link do
// not count, only the ones it opens afterwards.
func TestReusedWorkerEarlierPRsDoNotCloseALaterItem(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	e.writePRAt(sess.ID, 1, "merged", time.Now().Add(-time.Hour).UTC())
	it := e.item("second task", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.pump()
	if got := e.get(it); got.State != domain.BacklogOpen {
		t.Fatalf("closed by the worker's earlier PR: %s %q", got.State, got.ClosedReason)
	}
	if got := e.get(it); !strings.Contains(got.AutoClose, "before it was linked") {
		t.Fatalf("explanation = %q", got.AutoClose)
	}
	e.writePRAt(sess.ID, 2, "open", time.Now().Add(time.Minute).UTC())
	e.pump()
	e.writePRAt(sess.ID, 2, "merged", time.Now().Add(time.Minute).UTC())
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone || got.ClosedReason != "merged #2" {
		t.Fatalf("got %s %q", got.State, got.ClosedReason)
	}
}

// racyStore flips the item to scope local right after the lifecycle read its
// facts, as an orchestrator editing it while the merge lands would.
type racyStore struct {
	*sqlite.Store
	flipped bool
}

func (r *racyStore) BacklogFactsFor(ctx context.Context, itemID string) (store.BacklogFacts, error) {
	f, err := r.Store.BacklogFactsFor(ctx, itemID)
	if err == nil && !r.flipped {
		r.flipped = true
		_, err = r.UpdateBacklogItem(ctx, itemID, time.Now().UTC(), func(i *domain.BacklogItem) error {
			i.Scope = domain.BacklogScopeLocal
			return nil
		})
	}
	return f, err
}

// B45: scope set to local between the lifecycle's read and its close wins.
func TestScopeSetToLocalDuringTheMergeCloseIsRespected(t *testing.T) {
	e := newLifecycleEnv(t)
	e.svc = backlog.New(backlog.Deps{Store: &racyStore{Store: e.store}})
	e.sub = backlog.NewSubscriber(e.svc, backlog.SubscriberConfig{})
	sess := e.session()
	it := e.item("multi-PR", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 3, "open")
	e.writePR(sess.ID, 3, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogOpen || got.Scope != domain.BacklogScopeLocal {
		t.Fatalf("closed despite scope local: %s scope=%q", got.State, got.Scope)
	}
}

func TestAutoCloseDecisionsAreLoggedAtInfo(t *testing.T) {
	e, buf := loggedEnv(t)
	sess := e.session()
	closing := e.item("closes", "")
	e.link(closing, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 4, "merged")
	e.pump()
	line := buf.String()
	for _, want := range []string{"level=INFO", "item=" + closing.Ref(), "decision=closed", "reason=\"merged #4\"", "prs=[4]"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in log: %s", want, line)
		}
	}

	buf.Reset()
	other := e.session()
	local := e.item("held", domain.BacklogScopeLocal)
	e.link(local, domain.BacklogLinkSession, string(other.ID))
	e.writePR(other.ID, 5, "merged")
	e.pump()
	line = buf.String()
	for _, want := range []string{"level=INFO", "item=" + local.Ref(), `decision="kept open"`, "scope is local"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in log: %s", want, line)
		}
	}
	// The same verdict is not logged again on every session update.
	buf.Reset()
	e.svc.ReconcileLinked(e.ctx, domain.BacklogLinkSession, string(other.ID))
	if buf.Len() != 0 {
		t.Fatalf("repeated verdict logged again: %s", buf.String())
	}
}

func TestOpenItemWithMergedPRExplainsWhyItIsStillOpen(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("two prs", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writePR(sess.ID, 10, "merged")
	e.writePR(sess.ID, 11, "open")
	e.pump()
	got := e.get(it)
	if got.State != domain.BacklogOpen || got.AutoClose != "stays open: waiting for PR #11 open" {
		t.Fatalf("state=%s explanation=%q", got.State, got.AutoClose)
	}
	e.writePR(sess.ID, 11, "merged")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone || got.AutoClose != "" {
		t.Fatalf("closed item: state=%s explanation=%q", got.State, got.AutoClose)
	}
}

// A PR opened before the link and still open keeps a done report (without a
// pr_created output) from closing the item as having no PR.
func TestDoneReportKeepsItemOpenWhilePreLinkPRIsStillOpen(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	e.writePRAt(sess.ID, 6, "open", time.Now().Add(-time.Hour).UTC())
	it := e.item("linked after the pr", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	if err := e.svc.ReportFiled(e.ctx, domain.ReportRecord{SessionID: sess.ID, State: domain.ReportDone}, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.get(it); got.State != domain.BacklogOpen {
		t.Fatalf("closed by done report while PR #6 is open: %s %q", got.State, got.ClosedReason)
	}
}
