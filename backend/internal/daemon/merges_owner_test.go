package daemon

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// mergeOwner returns the spawning parent only while it is a live orchestrator.
func TestMergeOwnerResolvesLiveParentOrchestrator(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "p", Path: "/tmp/p", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	mk := func(kind domain.SessionKind, terminated bool) domain.SessionRecord {
		t.Helper()
		rec, err := store.CreateSession(ctx, domain.SessionRecord{
			ProjectID: "p", Kind: kind, Harness: domain.HarnessClaudeCode,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
			Metadata:  domain.SessionMetadata{Branch: "b", WorkspacePath: t.TempDir()},
			CreatedAt: now, UpdatedAt: now, IsTerminated: terminated,
		})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	worker := func(parent domain.SessionID) domain.SessionID {
		t.Helper()
		w := mk(domain.KindWorker, false)
		if parent != "" {
			if err := store.SetSessionLane(ctx, w.ID, domain.SessionLane{ParentSessionID: parent}); err != nil {
				t.Fatal(err)
			}
		}
		return w.ID
	}
	live, dead, sibling := mk(domain.KindOrchestrator, false), mk(domain.KindOrchestrator, true), mk(domain.KindWorker, false)
	owner := mergeOwner(store)
	for _, c := range []struct {
		name   string
		worker domain.SessionID
		want   domain.SessionID
		ok     bool
	}{
		{"live orchestrator parent", worker(live.ID), live.ID, true},
		{"terminated orchestrator parent", worker(dead.ID), "", false},
		{"worker parent", worker(sibling.ID), "", false},
		{"no parent", worker(""), "", false},
	} {
		got, ok := owner(ctx, "p", c.worker)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}
