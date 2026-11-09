package daemon

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// The daemon's engine, as wired: the trusted repo comes from the database.
func TestDaemonEngineResolvesFenceGitRepoFromTheDatabase(t *testing.T) {
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo/mer", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	create := func(meta domain.SessionMetadata) domain.SessionID {
		rec, err := store.CreateSession(ctx, domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now, Metadata: meta})
		if err != nil {
			t.Fatal(err)
		}
		return rec.ID
	}
	child := create(domain.SessionMetadata{WorkspaceRepoPath: "/repo/child"})
	plain := create(domain.SessionMetadata{})
	engine, err := newDaemonAgentPolicyEngine(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ref  agentpolicy.SessionRef
		want string
	}{
		{"recorded session repo wins", agentpolicy.SessionRef{ID: child, Project: "mer"}, "/repo/child"},
		{"session without one uses the project", agentpolicy.SessionRef{ID: plain, Project: "mer"}, "/repo/mer"},
		{"before the session exists", agentpolicy.SessionRef{Project: "mer"}, "/repo/mer"},
		{"unknown project grants nothing", agentpolicy.SessionRef{Project: "nope"}, ""},
		{"no project grants nothing", agentpolicy.SessionRef{}, ""},
	} {
		if got := engine.Decide(ctx, tc.ref).GitRepo(); got != tc.want {
			t.Errorf("%s: GitRepo = %q, want %q", tc.name, got, tc.want)
		}
	}
}
