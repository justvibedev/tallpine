package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// endCommander adds the workspace checks EndLane asks of the manager.
type endCommander struct {
	*fakeCommander
	settledErr error
	// removeErr is the result of the re-check right before teardown.
	removeErr error
	removed   []domain.SessionID
}

func (c *endCommander) CheckWorkspaceSettled(context.Context, domain.SessionID) error {
	return c.settledErr
}

func (c *endCommander) RemoveSettledWorkspace(_ context.Context, id domain.SessionID) error {
	if c.removeErr != nil {
		return c.removeErr
	}
	c.removed = append(c.removed, id)
	return nil
}

// endStore applies EndLane to the fake maps the way the SQL store does.
type endStore struct {
	*fakeStore
	ended []domain.SessionID
}

func (s *endStore) EndLane(_ context.Context, project domain.ProjectID, lane string, orch domain.SessionID) error {
	for id, l := range s.lanes {
		if l.LaneItemID == lane && id != orch && s.sessions[id].ProjectID == project {
			l.LaneItemID = ""
			s.lanes[id] = l
		}
	}
	delete(s.sessions, orch)
	delete(s.lanes, orch)
	s.ended = append(s.ended, orch)
	return nil
}

type endFixture struct {
	svc *Service
	st  *endStore
	cmd *endCommander
}

// newEndFixture: default orchestrator mer-1, lane-a orchestrator mer-2 with two
// live lane-a workers, one live lane-b worker and one dead lane-a worker.
func newEndFixture(t *testing.T) *endFixture {
	t.Helper()
	st := &endStore{fakeStore: laneStore(t)}
	for id, lane := range map[domain.SessionID]string{"w-a1": "lane-a", "w-a2": "lane-a", "w-b": "lane-b", "w-dead": "lane-a"} {
		st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", Kind: domain.KindWorker, IsTerminated: id == "w-dead"}
		st.lanes[id] = domain.SessionLane{LaneItemID: lane}
	}
	fc := &fakeCommander{}
	fc.killFunc = func(id domain.SessionID) {
		rec := st.sessions[id]
		rec.IsTerminated = true
		st.sessions[id] = rec
	}
	cmd := &endCommander{fakeCommander: fc}
	return &endFixture{svc: &Service{manager: cmd, store: st, dataDir: t.TempDir()}, st: st, cmd: cmd}
}

func TestEndLaneRemovesTheOrchestratorAndRehomesLiveWorkers(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	out, err := f.svc.EndLane(context.Background(), "mer-2", false)
	if err != nil {
		t.Fatalf("EndLane: %v", err)
	}
	if out.Orchestrator != "mer-1" || f.cmd.spawned {
		t.Fatalf("workers moved to %s (spawned=%v), want the running default mer-1", out.Orchestrator, f.cmd.spawned)
	}
	if len(f.cmd.killed) != 1 || f.cmd.killed[0] != "mer-2" || len(f.cmd.removed) != 1 {
		t.Fatalf("killed=%v removed=%v, want mer-2 stopped and its workspace removed", f.cmd.killed, f.cmd.removed)
	}
	if _, still := f.st.sessions["mer-2"]; still || len(f.st.ended) != 1 {
		t.Fatal("the lane orchestrator's record was not removed")
	}
	for _, w := range []domain.SessionID{"w-a1", "w-a2", "w-dead"} {
		if got := f.st.lanes[w].LaneItemID; got != "" {
			t.Errorf("%s still in lane %q", w, got)
		}
	}
	if f.st.lanes["w-b"].LaneItemID != "lane-b" {
		t.Error("a worker of another lane was moved")
	}
	if len(out.Workers) != 2 || len(f.cmd.sent) != 2 {
		t.Fatalf("workers=%v notified=%v, want exactly the two live lane-a workers", out.Workers, f.cmd.sent)
	}
	for i, msg := range f.cmd.sentMessages {
		if !strings.Contains(msg, "mer-1") || !strings.Contains(msg, "mer-2") {
			t.Errorf("notice %d = %q, want it to name the new (mer-1) and removed (mer-2) orchestrators", i, msg)
		}
	}
}

func TestEndLaneStartsTheDefaultOrchestratorWhenNoneRuns(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	delete(f.st.sessions, "mer-1")
	f.cmd.spawnFunc = func(cfg ports.SpawnConfig) domain.SessionRecord {
		rec := domain.SessionRecord{ID: "mer-new", ProjectID: cfg.ProjectID, Kind: cfg.Kind}
		f.st.sessions[rec.ID] = rec
		return rec
	}
	out, err := f.svc.EndLane(context.Background(), "mer-2", false)
	if err != nil {
		t.Fatalf("EndLane: %v", err)
	}
	if !f.cmd.spawned || f.cmd.spawnedCfg.LaneItemID != "" || f.cmd.spawnedCfg.Kind != domain.KindOrchestrator {
		t.Fatalf("spawned=%v cfg=%+v, want an un-laned orchestrator", f.cmd.spawned, f.cmd.spawnedCfg)
	}
	if out.Orchestrator != "mer-new" || !strings.Contains(f.cmd.sentMessages[0], "mer-new") {
		t.Fatalf("workers moved to %q, notice %q; want the new default mer-new", out.Orchestrator, f.cmd.sentMessages[0])
	}
}

func TestEndLaneRefusesAWorktreeWithWorkAndChangesNothing(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	f.cmd.settledErr = fmt.Errorf("%w: 3 uncommitted path(s)", sessionmanager.ErrWorkspaceHoldsWork)
	_, err := f.svc.EndLane(context.Background(), "mer-2", false)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "LANE_WORKSPACE_HAS_WORK" {
		t.Fatalf("err = %v, want LANE_WORKSPACE_HAS_WORK", err)
	}
	if strings.Contains(ae.Message, "left stopped") {
		t.Fatalf("message = %q claims a stop that did not happen", ae.Message)
	}
	if len(f.cmd.killed) != 0 || len(f.st.ended) != 0 || len(f.cmd.sent) != 0 || f.cmd.spawned {
		t.Fatalf("a refused end changed state: killed=%v ended=%v sent=%v spawned=%v", f.cmd.killed, f.st.ended, f.cmd.sent, f.cmd.spawned)
	}
	if f.st.lanes["w-a1"].LaneItemID != "lane-a" {
		t.Fatal("workers were moved although the end was refused")
	}
}

func TestEndLaneRefusesTheDefaultOrchestratorAndWorkers(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	for _, id := range []domain.SessionID{"mer-1", "w-a1"} {
		if _, err := f.svc.EndLane(context.Background(), id, false); err == nil {
			t.Errorf("EndLane(%s) succeeded, want a refusal", id)
		}
	}
	if len(f.cmd.killed) != 0 || len(f.st.ended) != 0 {
		t.Fatal("a refused end changed state")
	}
}

func TestEndLaneDryRunCountsWorkersAndChangesNothing(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	out, err := f.svc.EndLane(context.Background(), "mer-2", true)
	if err != nil || len(out.Workers) != 2 {
		t.Fatalf("dry run = %+v err=%v, want the two live lane-a workers", out, err)
	}
	if len(f.cmd.killed) != 0 || len(f.st.ended) != 0 || len(f.cmd.sent) != 0 || f.cmd.spawned {
		t.Fatal("a dry run changed state")
	}
	f.cmd.settledErr = fmt.Errorf("%w: dirty", sessionmanager.ErrWorkspaceHoldsWork)
	if _, err := f.svc.EndLane(context.Background(), "mer-2", true); err == nil {
		t.Fatal("a dry run must surface the unsaved-work refusal")
	}
}

// An archived lane orchestrator whose kept worktree holds work must not lose
// its record on a retry.
func TestEndLaneRefusesATerminatedOrchestratorWithAWorktreeHoldingWork(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	rec := f.st.sessions["mer-2"]
	rec.IsTerminated = true
	f.st.sessions["mer-2"] = rec
	f.cmd.settledErr = fmt.Errorf("%w: 2 uncommitted path(s)", sessionmanager.ErrWorkspaceHoldsWork)
	_, err := f.svc.EndLane(context.Background(), "mer-2", false)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "LANE_WORKSPACE_HAS_WORK" {
		t.Fatalf("err = %v, want LANE_WORKSPACE_HAS_WORK", err)
	}
	if _, kept := f.st.sessions["mer-2"]; !kept || len(f.st.ended) != 0 || len(f.cmd.removed) != 0 {
		t.Fatal("the row was removed or the worktree deleted although it holds work")
	}
}

// Work committed after the first check (the agent was still running) is caught
// by the re-check once the agent is stopped: the row is kept.
func TestEndLaneRefusesWorkAppearingAfterTheFirstCheck(t *testing.T) {
	t.Parallel()
	f := newEndFixture(t)
	f.cmd.removeErr = fmt.Errorf("%w: branch has commits that are not in the base branch", sessionmanager.ErrWorkspaceHoldsWork)
	_, err := f.svc.EndLane(context.Background(), "mer-2", false)
	var ae *apierr.Error
	if !errors.As(err, &ae) || ae.Code != "LANE_WORKSPACE_HAS_WORK" {
		t.Fatalf("err = %v, want LANE_WORKSPACE_HAS_WORK", err)
	}
	// The agent was already killed: the message must say it was left stopped and how to resume.
	if !strings.Contains(ae.Message, "left stopped") || !strings.Contains(ae.Message, "mer-2") || !strings.Contains(ae.Message, "Resume agent") {
		t.Fatalf("message = %q, want it to say mer-2 was left stopped and how to resume", ae.Message)
	}
	if _, kept := f.st.sessions["mer-2"]; !kept || len(f.st.ended) != 0 || len(f.cmd.sent) != 0 {
		t.Fatal("state changed although the re-check refused")
	}
	if f.st.lanes["w-a1"].LaneItemID != "lane-a" {
		t.Fatal("workers were moved although the end failed")
	}
}

func TestAgentEndLaneRefusedWhileAWorkerHasAnOpenPR(t *testing.T) {
	t.Parallel()
	agent := WithAgentLane(context.Background(), AgentLaneRequest{RefuseOpenPR: true})
	open := domain.PullRequest{URL: "u"}
	for _, tc := range []struct {
		name    string
		worker  domain.SessionID
		pr      domain.PullRequest
		wantErr bool
	}{
		{"live worker open PR", "w-a1", open, true},
		{"terminated worker open PR", "w-dead", open, true},
		{"merged PR", "w-a1", domain.PullRequest{URL: "u", Merged: true}, false},
		{"closed PR", "w-a1", domain.PullRequest{URL: "u", Closed: true}, false},
		{"other lane's open PR", "w-b", open, false},
	} {
		f := newEndFixture(t)
		f.st.prs = map[domain.SessionID][]domain.PullRequest{tc.worker: {tc.pr}}
		_, err := f.svc.EndLane(agent, "mer-2", false)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
		if tc.wantErr && (len(f.st.ended) != 0 || !strings.Contains(err.Error(), "open PR")) {
			t.Errorf("%s: ended=%v err=%v, want refused with nothing changed", tc.name, f.st.ended, err)
		}
	}
	f := newEndFixture(t)
	f.st.prs = map[domain.SessionID][]domain.PullRequest{"w-a1": {open}}
	if _, err := f.svc.EndLane(context.Background(), "mer-2", false); err != nil {
		t.Errorf("owner end blocked by an open PR: %v", err)
	}
}
