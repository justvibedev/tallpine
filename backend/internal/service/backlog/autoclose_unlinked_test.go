package backlog_test

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// A qualifier in another clause (B615, #1101) does not cancel a claim; one
// before it, or in its own clause, still does.
func TestClaimQualifierIsScopedToItsClause(t *testing.T) {
	cases := []struct {
		title, ref string
		want       bool
	}{
		{"closes B401, part of B600", "B401", true},
		{"closes B401 part of B600", "B401", true},
		{"closes B401 (part 1)", "B401", false},
		{"closes B401 partially", "B401", false},
		{"closes B613, closes B556", "B556", true},
		{"closes B613, closes B556", "B613", true},
		{"part of B600, closes B401", "B401", true},
		{"partly closes B401", "B401", false},
		{"closes B401, partly fixes B600", "B401", true},
		{"closes B401, part 2 of B600", "B401", true},
		{"closes B401, partial", "B401", false},
	}
	for _, c := range cases {
		if got := backlog.TitleClaims(c.title, c.ref); got != c.want {
			t.Errorf("%q %s: %v want %v", c.title, c.ref, got, c.want)
		}
	}
	if got := backlog.ClaimedRefs("closes B613, closes B556"); len(got) != 2 || got[0] != "B613" || got[1] != "B556" {
		t.Errorf("ClaimedRefs = %v", got)
	}
}

// A merged PR linked to one item also closes the unlinked item it claims.
func TestMergedPRClosesClaimedItemItIsNotLinkedTo(t *testing.T) {
	for _, tc := range []struct {
		title      string
		wantClosed bool
	}{
		{"fix: x (closes B1, closes B2)", true},
		{"fix: x (closes B1, part of B2)", false},
		{"fix: x (closes B1, closes B3)", false}, // B3 does not exist; B2 untouched
	} {
		e := newLifecycleEnv(t)
		sess := e.session()
		linked := e.item("linked", "")
		other := e.item("other", "")
		e.link(linked, domain.BacklogLinkSession, string(sess.ID))
		e.writeMergedPR(sess.ID, 9, tc.title)
		e.pump()
		if got := e.get(linked); got.State != domain.BacklogDone {
			t.Fatalf("%q: linked item state %s", tc.title, got.State)
		}
		if got := e.get(other).State == domain.BacklogDone; got != tc.wantClosed {
			t.Errorf("%q: unlinked item closed=%v want %v", tc.title, got, tc.wantClosed)
		}
	}
}

// A reopened item is not closed again by the old PR that claimed it.
// A claim never closes a scope-local item.
func TestClaimedItemScopeLocalStaysOpen(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	linked := e.item("linked", "")
	local := e.item("multi-PR", domain.BacklogScopeLocal)
	e.link(linked, domain.BacklogLinkSession, string(sess.ID))
	e.writeMergedPR(sess.ID, 9, "closes B1, closes B2")
	e.pump()
	if got := e.get(local); got.State == domain.BacklogDone {
		t.Fatalf("scope local item closed by a claim")
	}
}

// A manual reopen sticks: a later event for the claiming PR does not close it again.
func TestReopenedClaimedItemStaysOpen(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	linked := e.item("linked", "")
	other := e.item("other", "")
	e.link(linked, domain.BacklogLinkSession, string(sess.ID))
	e.writeMergedPR(sess.ID, 9, "closes B1, closes B2")
	e.pump()
	if got := e.get(other); got.State != domain.BacklogDone {
		t.Fatalf("not closed by claim: %s", got.State)
	}
	if _, err := e.svc.SetState(e.ctx, "p1", other.ID, domain.BacklogOpen, ""); err != nil {
		t.Fatal(err)
	}
	// Any later event for the merged PR (CI state, review decision) re-enters the claim path.
	if err := e.svc.ReconcileLinked(e.ctx, domain.BacklogLinkPR, "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if got := e.get(other); got.State != domain.BacklogOpen {
		t.Fatalf("reopened item closed again: %s", got.State)
	}
}

// A PR the worker opened before it was linked to the item does not count for
// Reconcile, but its title still claims the item.
func TestClaimClosesItemWhosePRPredatesTheSessionLink(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	e.item("first", "")
	b := e.item("second", "")
	pr := domain.PullRequest{
		URL: "https://github.com/o/r/pull/9", SessionID: sess.ID, Number: 9, Title: "closes B1, closes B2",
		UpdatedAt: time.Now().UTC(), Merged: true, CreatedAtProvider: time.Now().Add(-time.Hour).UTC(),
	}
	if err := e.store.WriteSCMObservation(e.ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	e.link(b, domain.BacklogLinkSession, string(sess.ID))
	e.pump()
	if got := e.get(b); got.State != domain.BacklogDone {
		t.Fatalf("state %s", got.State)
	}
}
