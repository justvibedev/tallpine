package cleanup

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeBacklog struct {
	items  []domain.BacklogItem
	closed map[string]string
	// touched ids were accepted or edited between the listing and the close.
	touched map[string]bool
	cutoff  time.Time
}

func (f *fakeBacklog) ListExpiredBacklogInbox(_ context.Context, cutoff time.Time) ([]domain.BacklogItem, error) {
	f.cutoff = cutoff
	return f.items, nil
}

func (f *fakeBacklog) ExpireBacklogInboxItem(_ context.Context, id string, _ time.Time, reason string, _ time.Time) (domain.BacklogItem, bool, error) {
	if f.touched[id] {
		return domain.BacklogItem{ID: id}, false, nil
	}
	if f.closed == nil {
		f.closed = map[string]string{}
	}
	f.closed[id] = "dropped:" + reason
	return domain.BacklogItem{ID: id}, true, nil
}

func backlogItem(id string, kind domain.BacklogOriginKind, state domain.BacklogState) domain.BacklogItem {
	return domain.BacklogItem{ID: id, ProjectID: "mer", Seq: 1, Title: id, State: state, Origin: domain.BacklogOrigin{Kind: kind}}
}

func TestBacklogRuleDropsAgentInboxItemsButNeverHumanOnes(t *testing.T) {
	inv := &fakeBacklog{items: []domain.BacklogItem{
		backlogItem("worker", domain.BacklogOriginWorker, domain.BacklogInbox),
		backlogItem("orchestrator", domain.BacklogOriginOrchestrator, domain.BacklogInbox),
		backlogItem("reviewer", domain.BacklogOriginReviewer, domain.BacklogInbox),
		backlogItem("human", domain.BacklogOriginHuman, domain.BacklogInbox),
		backlogItem("already-open", domain.BacklogOriginWorker, domain.BacklogOpen),
	}}
	store := &fakeStore{}
	report := runRuntimeRules(t, store, RunRequest{}, NewBacklogRule(inv))
	if len(inv.closed) != 3 {
		t.Fatalf("closed = %v, want worker, orchestrator and reviewer only", inv.closed)
	}
	for _, id := range []string{"worker", "orchestrator", "reviewer"} {
		if inv.closed[id] != "dropped:expired" {
			t.Errorf("%s closed as %q, want dropped:expired", id, inv.closed[id])
		}
	}
	if _, ok := inv.closed["human"]; ok {
		t.Error("a human-filed item was expired")
	}
	if got := report.Categories[0]; got.Category != CategoryBacklog || got.Freed != 3 {
		t.Fatalf("report = %+v", got)
	}
	if want := runtimeNow.Add(-14 * 24 * time.Hour); !inv.cutoff.Equal(want) {
		t.Fatalf("cutoff = %v, want %v (14 days by default)", inv.cutoff, want)
	}
}

func TestBacklogRuleDryRunDisabledAndCustomDays(t *testing.T) {
	item := backlogItem("worker", domain.BacklogOriginWorker, domain.BacklogInbox)

	dry := &fakeBacklog{items: []domain.BacklogItem{item}}
	report := runRuntimeRules(t, &fakeStore{}, RunRequest{DryRun: true}, NewBacklogRule(dry))
	if len(dry.closed) != 0 || report.Categories[0].WouldFree != 1 {
		t.Fatalf("dry run closed=%v report=%+v", dry.closed, report.Categories[0])
	}

	off := &fakeBacklog{items: []domain.BacklogItem{item}}
	runRuntimeRules(t, &fakeStore{policy: `{"backlog":{"enabled":false}}`}, RunRequest{}, NewBacklogRule(off))
	if len(off.closed) != 0 {
		t.Fatalf("disabled policy still expired %v", off.closed)
	}

	custom := &fakeBacklog{items: []domain.BacklogItem{item}}
	runRuntimeRules(t, &fakeStore{policy: `{"backlog":{"inboxExpiryDays":30}}`}, RunRequest{}, NewBacklogRule(custom))
	if want := runtimeNow.Add(-30 * 24 * time.Hour); !custom.cutoff.Equal(want) {
		t.Fatalf("cutoff = %v, want %v", custom.cutoff, want)
	}
}

func TestBacklogPolicyDefaultsAndValidation(t *testing.T) {
	p := DefaultPolicy().Backlog
	if !p.Enabled || p.InboxExpiryDays != 14 {
		t.Fatalf("default = %+v, want enabled with 14 days", p)
	}
	// A stored policy written before the backlog existed keeps the default.
	if got := DecodePolicy(`{"worktrees":{"enabled":true,"retentionDays":7}}`).Backlog; got != p {
		t.Fatalf("old document decodes to %+v", got)
	}
	if _, err := decodePolicy(`{"backlog":{"enabled":true,"inboxExpiryDays":0}}`); err == nil || !strings.Contains(err.Error(), "inboxExpiryDays") {
		t.Fatalf("zero days accepted: %v", err)
	}
}

func TestBacklogRuleKeepsAnItemTriagedBetweenListAndClose(t *testing.T) {
	inv := &fakeBacklog{
		items:   []domain.BacklogItem{backlogItem("accepted", domain.BacklogOriginWorker, domain.BacklogInbox), backlogItem("stale", domain.BacklogOriginWorker, domain.BacklogInbox)},
		touched: map[string]bool{"accepted": true},
	}
	report := runRuntimeRules(t, &fakeStore{}, RunRequest{}, NewBacklogRule(inv))
	if _, dropped := inv.closed["accepted"]; dropped || inv.closed["stale"] == "" {
		t.Fatalf("closed = %v", inv.closed)
	}
	if got := report.Categories[0]; got.Freed != 1 || got.Kept != 1 {
		t.Fatalf("report = %+v, want 1 freed and 1 kept", got)
	}
}
