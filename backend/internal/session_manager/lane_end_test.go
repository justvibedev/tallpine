package sessionmanager

import (
	"context"
	"errors"
	"os"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// observingWorkspace answers each observation from obs in turn (the last one
// repeats) and records force-removals and branch deletions.
type observingWorkspace struct {
	ports.Workspace
	obs       []ports.WorkspaceObservation
	err       error
	calls     int
	destroyed []ports.WorkspaceInfo
	branches  []ports.WorkspaceInfo
}

func (w *observingWorkspace) ObserveWorkspace(context.Context, ports.WorkspaceInfo) (ports.WorkspaceObservation, error) {
	i := min(w.calls, len(w.obs)-1)
	w.calls++
	return w.obs[i], w.err
}

func (w *observingWorkspace) ForceDestroy(_ context.Context, info ports.WorkspaceInfo) error {
	w.destroyed = append(w.destroyed, info)
	return nil
}

func (w *observingWorkspace) DeletePreparedBranch(_ context.Context, info ports.WorkspaceInfo) error {
	w.branches = append(w.branches, info)
	return nil
}

type settledStore struct {
	Store
	rec domain.SessionRecord
}

func (s settledStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.rec, true, nil
}

func TestCheckWorkspaceSettled(t *testing.T) {
	dir := t.TempDir()
	rec := domain.SessionRecord{ID: "s", IsTerminated: true, Metadata: domain.SessionMetadata{WorkspacePath: dir, DiffBaseSHA: "base"}}
	cases := []struct {
		name    string
		obs     ports.WorkspaceObservation
		obsErr  error
		path    string
		holds   bool
		wantErr bool
	}{
		{name: "clean at creation commit", obs: ports.WorkspaceObservation{HeadSHA: "base"}},
		{name: "uncommitted changes", obs: ports.WorkspaceObservation{HeadSHA: "base", Dirty: true}, holds: true},
		{name: "untracked files", obs: ports.WorkspaceObservation{HeadSHA: "base", Untracked: true}, holds: true},
		{name: "unlanded commits", obs: ports.WorkspaceObservation{HeadSHA: "new"}, holds: true},
		{name: "commits landed in base", obs: ports.WorkspaceObservation{HeadSHA: "new", Landed: true}},
		{name: "unobservable fails closed", obs: ports.WorkspaceObservation{}, obsErr: errors.New("boom"), wantErr: true},
		{name: "worktree already gone", path: dir + "/missing", obs: ports.WorkspaceObservation{Dirty: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := rec // terminated, as for a retried end
			if tc.path != "" {
				r.Metadata.WorkspacePath = tc.path
			}
			m := &Manager{store: settledStore{rec: r}, workspace: &observingWorkspace{obs: []ports.WorkspaceObservation{tc.obs}, err: tc.obsErr}}
			err := m.CheckWorkspaceSettled(context.Background(), "s")
			switch {
			case tc.holds && !errors.Is(err, ErrWorkspaceHoldsWork):
				t.Fatalf("err = %v, want ErrWorkspaceHoldsWork", err)
			case tc.wantErr && (err == nil || errors.Is(err, ErrWorkspaceHoldsWork)):
				t.Fatalf("err = %v, want a plain error", err)
			case !tc.holds && !tc.wantErr && err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveSettledWorkspaceRechecksBeforeForceDestroy(t *testing.T) {
	rec := domain.SessionRecord{ID: "s", Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir(), DiffBaseSHA: "base", Branch: "vo/orch"}}
	run := func(obs ...ports.WorkspaceObservation) (*observingWorkspace, error) {
		ws := &observingWorkspace{obs: obs}
		m := &Manager{store: settledStore{rec: rec}, workspace: ws}
		return ws, m.RemoveSettledWorkspace(context.Background(), "s")
	}
	// A commit made after an earlier check shows up now: nothing is destroyed.
	ws, err := run(ports.WorkspaceObservation{HeadSHA: "new"})
	if !errors.Is(err, ErrWorkspaceHoldsWork) || len(ws.destroyed) != 0 || len(ws.branches) != 0 {
		t.Fatalf("unlanded commit: err=%v destroyed=%d branches=%d, want a refusal that removes nothing", err, len(ws.destroyed), len(ws.branches))
	}
	ws, err = run(ports.WorkspaceObservation{HeadSHA: "base", Dirty: true})
	if !errors.Is(err, ErrWorkspaceHoldsWork) || len(ws.destroyed) != 0 {
		t.Fatalf("dirty: err=%v destroyed=%d, want a refusal", err, len(ws.destroyed))
	}
	// Unmoved: the worktree is force-removed and the branch deleted at its creation commit.
	ws, err = run(ports.WorkspaceObservation{HeadSHA: "base"})
	if err != nil || len(ws.destroyed) != 1 || len(ws.branches) != 1 || ws.branches[0].BaseSHA != "base" {
		t.Fatalf("unmoved: err=%v destroyed=%d branches=%+v", err, len(ws.destroyed), ws.branches)
	}
	// Landed in the base: the branch goes too, matched at its observed tip.
	ws, err = run(ports.WorkspaceObservation{HeadSHA: "tip", Landed: true})
	if err != nil || len(ws.destroyed) != 1 || len(ws.branches) != 1 || ws.branches[0].BaseSHA != "tip" {
		t.Fatalf("landed: err=%v destroyed=%d branches=%+v, want the branch deleted at tip", err, len(ws.destroyed), ws.branches)
	}
}
