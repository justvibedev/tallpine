package backlog_test

import (
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// writeMergedPR writes a merged PR with the given title.
func (e *lifecycleEnv) writeMergedPR(sess domain.SessionID, number int, title string) {
	e.t.Helper()
	pr := domain.PullRequest{
		URL: fmt.Sprintf("https://github.com/o/r/pull/%d", number), SessionID: sess, Number: number,
		Title: title, UpdatedAt: time.Now().UTC(), Merged: true,
	}
	if err := e.store.WriteSCMObservation(e.ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		e.t.Fatal(err)
	}
}

func (e *lifecycleEnv) itemWithDoneWhen(doneWhen string) backlog.View {
	e.t.Helper()
	v, err := e.svc.Create(e.ctx, "p1", backlog.Actor{Kind: domain.BacklogOriginHuman}, backlog.CreateInput{Title: "t", DoneWhen: doneWhen})
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// A merged PR that does not claim the done-when leaves the item open.
func TestPartialPRDoesNotCloseItemWithDoneWhen(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.itemWithDoneWhen("several days of readings")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writeMergedPR(sess.ID, 5, "fix(x): first part of "+e.get(it).Ref())
	e.pump()
	got := e.get(it)
	if got.State == domain.BacklogDone {
		t.Fatalf("partial PR closed the item: %q", got.ClosedReason)
	}
	if d := backlog.DecideAutoClose(got.BacklogItem, mustFacts(t, e, got)); d.Close || d.Reason == "" {
		t.Fatalf("decision %+v", d)
	}
}

// A merged PR that claims the item closes it, done-when or not.
func TestClaimingPRClosesItemWithDoneWhen(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.itemWithDoneWhen("it works")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writeMergedPR(sess.ID, 6, "fix(x): stop the bug (Closes "+e.get(it).Ref()+")")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone {
		t.Fatalf("state %s", got.State)
	}
}

// An item without a done-when keeps closing on any merge.
func TestMergeClosesItemWithoutDoneWhen(t *testing.T) {
	e := newLifecycleEnv(t)
	sess := e.session()
	it := e.item("plain", "")
	e.link(it, domain.BacklogLinkSession, string(sess.ID))
	e.writeMergedPR(sess.ID, 7, "anything")
	e.pump()
	if got := e.get(it); got.State != domain.BacklogDone {
		t.Fatalf("state %s", got.State)
	}
}

func mustFacts(t *testing.T, e *lifecycleEnv, v backlog.View) store.BacklogFacts {
	t.Helper()
	f, err := e.store.BacklogFactsFor(e.ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Only an unqualified "closes <ref>" is a full claim.
func TestOnlyUnqualifiedClosesClaimsDoneWhen(t *testing.T) {
	cases := map[string]bool{
		"fix(x): stop the bug (closes B1)": true,
		"Closes B1: stop the bug":          true,
		"partially fixes B1":               false,
		"partly closes B1":                 false,
		"fix B1 flake, part 1":             false,
		"does not close B1":                false,
		"does not closes B1":               false,
		"fixes B1":                         false,
		"resolves B1":                      false,
		"closes B10":                       false,
		"closes B1 part 1":                 false,
		"feat: x (closes B1, partial)":     false,
		"closes B1 partially":              false,
		"fix(editor): keep partial drafts on reload (closes B1)": true,
		"fix(ui): remove WIP banner (closes B1)":                 true,
		"Part of B226, closes B1":                                true,
		"fix(x): partial fix, closes B1":                         false,
		"feat(x): Part 1: add parser, closes B1":                 false,
		"fix(x): part 1 of 3: closes B1":                         false,
		"fix(x)!: Part 1/2: closes B1":                           false,
		"feat: add parser (part 1/3), closes B1":                 false,
		"feat: add parser, part 2 of 3: closes B1":               false,
		"Part 1: closes B1":                                      false,
		"WIP: closes B1":                                         false,
		"(partial) closes B1":                                    false,
		"feat: x (B226, closes B1)":                              true,
	}
	for title, want := range cases {
		prs := []store.BacklogPRFact{{State: "merged", Title: title}}
		item := domain.BacklogItem{Seq: 1, DoneWhen: "x"}
		if got := backlog.DecideAutoClose(item, store.BacklogFacts{PRs: prs}).Close; got != want {
			t.Errorf("%q: close=%v want %v", title, got, want)
		}
	}
}

// A PR that claims a plan's child leaves the parent open although the author's
// session is linked to both.
func TestPRClaimingChildDoesNotCloseParentWithDoneWhen(t *testing.T) {
	parent := domain.BacklogItem{Seq: 1, DoneWhen: "all PRs landed"}
	prs := []store.BacklogPRFact{{State: "merged", Title: "feat: backend (closes B2)"}}
	if d := backlog.DecideAutoClose(parent, store.BacklogFacts{PRs: prs}); d.Close {
		t.Fatalf("child claim closed the parent: %+v", d)
	}
	child := domain.BacklogItem{Seq: 2, DoneWhen: "TestA"}
	if d := backlog.DecideAutoClose(child, store.BacklogFacts{PRs: prs}); !d.Close {
		t.Fatalf("child claim did not close the child: %+v", d)
	}
}
