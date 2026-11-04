package lifecycle

import (
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestMarkTerminated_ReapsContainers is the #2652 regression for hooking the
// shared teardown path: MarkTerminated must reap the terminated session's
// containers, covering every terminal-state path (Kill, daemon shutdown,
// Cleanup, RetireForReplacement, tracker-driven termination) through this one
// choke point rather than only explicit tallpine session kill.
func TestMarkTerminated_ReapsContainers(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{removed: 2}
	pl := &fakeProjectConfigLoader{projects: map[string]domain.ProjectRecord{
		"mer": {ID: "mer", Config: domain.ProjectConfig{}},
	}}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(cr.sessions) != 1 || cr.sessions[0] != "mer-1" {
		t.Fatalf("expected container reap for mer-1, got %v", cr.sessions)
	}
}

func TestMarkTerminated_ReapsStandaloneSessionContainers(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{}
	// A standalone session has no project config to load. Make any accidental
	// lookup fail so the test proves the default reap-enabled policy bypasses it.
	pl := &fakeProjectConfigLoader{err: errors.New("standalone has no project")}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	rec := working("standalone-1")
	rec.ProjectID = ""
	st.sessions[rec.ID] = rec

	if err := m.MarkTerminated(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if len(cr.sessions) != 1 || cr.sessions[0] != rec.ID {
		t.Fatalf("expected container reap for standalone session %q, got %v", rec.ID, cr.sessions)
	}
}

func TestMarkTerminated_ReapsContainersAgainWhenAlreadyTerminated(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{}
	pl := &fakeProjectConfigLoader{projects: map[string]domain.ProjectRecord{
		"mer": {ID: "mer", Config: domain.ProjectConfig{}},
	}}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(cr.sessions) != 2 {
		t.Fatalf("container reap calls = %v, want retry on repeated termination", cr.sessions)
	}
}

// TestMarkTerminated_ContainerReapFailureDoesNotFailTermination asserts the
// best-effort contract: a container reaper error must never fail
// MarkTerminated, matching every other best-effort teardown step in Tallpine.
func TestMarkTerminated_ContainerReapFailureDoesNotFailTermination(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{err: errors.New("docker rm: permission denied")}
	pl := &fakeProjectConfigLoader{projects: map[string]domain.ProjectRecord{
		"mer": {ID: "mer", Config: domain.ProjectConfig{}},
	}}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatalf("a container reap failure must not fail MarkTerminated: %v", err)
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated {
		t.Fatal("session must still be marked terminated despite the reap failure")
	}
	if len(cr.sessions) != 1 {
		t.Fatalf("expected container reap to still be attempted, got %v", cr.sessions)
	}
}

// TestMarkTerminated_SkipsReapWhenProjectDisables covers the project-level
// opt-out: ContainerReap.Disabled must suppress the reap without affecting
// termination.
func TestMarkTerminated_SkipsReapWhenProjectDisables(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{}
	pl := &fakeProjectConfigLoader{projects: map[string]domain.ProjectRecord{
		"mer": {ID: "mer", Config: domain.ProjectConfig{ContainerReap: domain.ContainerReapConfig{Disabled: true}}},
	}}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(cr.sessions) != 0 {
		t.Fatalf("expected no reap call when project disables container reap, got %v", cr.sessions)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must still be marked terminated when reap is disabled")
	}
}

// TestMarkTerminated_ProjectLoadErrorSkipsRatherThanReaps is the regression
// for failing open: a project-config load error must skip reaping rather than
// guess and reap anyway. This package's stated bias throughout is to spare on
// ambiguity, never to reap on it.
func TestMarkTerminated_ProjectLoadErrorSkipsRatherThanReaps(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{}
	pl := &fakeProjectConfigLoader{err: errors.New("db unavailable")}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(cr.sessions) != 0 {
		t.Fatalf("a project-load error must skip reaping (spare on ambiguity), got calls: %v", cr.sessions)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must still be marked terminated when the project lookup fails")
	}
}

// TestMarkTerminated_NilReaperSkipsWithoutProjectLookup confirms nil wiring
// (the common case — most Tallpine installs run without Docker) skips reaping
// cleanly without even attempting a project lookup.
func TestMarkTerminated_NilReaperSkipsWithoutProjectLookup(t *testing.T) {
	m, st, _ := newManager() // newManager wires no container reaper at all
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must still be marked terminated with no reaper wired")
	}
}

// TestMarkTerminated_MissingProjectSkipsRatherThanReaps is the regression for
// failing open on a missing project record: GetProject returning ok=false,
// err=nil is ambiguity (Tallpine cannot know whether ContainerReap.Disabled would
// have applied), not a green light to reap. Must be treated the same as the
// error path.
func TestMarkTerminated_MissingProjectSkipsRatherThanReaps(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{}
	pl := &fakeProjectConfigLoader{projects: map[string]domain.ProjectRecord{}} // no "mer" entry: ok=false, err=nil
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	st.sessions["mer-1"] = working("mer-1")

	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(cr.sessions) != 0 {
		t.Fatalf("a missing project record must skip reaping (spare on ambiguity), got calls: %v", cr.sessions)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("session must still be marked terminated when the project record is missing")
	}
}

// TestRuntimeObservation_ConfirmedDeathReapsContainers is the regression for
// the review finding that ApplyRuntimeObservation's reaper-driven terminal
// transition (crash/SIGKILL detected by the runtime reaper) bypassed
// MarkTerminated entirely and left containers unreaped. This confirms the
// container leg of #2652 now fires on this path too, not just explicit kill.
func TestRuntimeObservation_ConfirmedDeathReapsContainers(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{removed: 1}
	pl := &fakeProjectConfigLoader{projects: map[string]domain.ProjectRecord{
		"mer": {ID: "mer", Config: domain.ProjectConfig{}},
	}}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	st.sessions["mer-1"] = rec

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("want terminated/exited, got %+v", got)
	}
	if len(cr.sessions) != 1 || cr.sessions[0] != "mer-1" {
		t.Fatalf("expected container reap for mer-1 on reaper-observed death, got %v", cr.sessions)
	}
}

// TestRuntimeObservation_WorkloadDeathAloneDoesNotReap confirms the
// non-terminal workload-dead branch (runtime alive, workload dead) does NOT
// trigger a container reap — only a confirmed session termination should.
func TestRuntimeObservation_WorkloadDeathAloneDoesNotReap(t *testing.T) {
	cr := &fakeLifecycleContainerReaper{}
	pl := &fakeProjectConfigLoader{projects: map[string]domain.ProjectRecord{
		"mer": {ID: "mer", Config: domain.ProjectConfig{}},
	}}
	m, st, _ := newManagerWithContainerReaper(cr, pl)
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	st.sessions["mer-1"] = rec

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{LaunchID: "launch-1", Runtime: ports.ProbeAlive, Workload: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated {
		t.Fatal("workload death alone must not terminate the session")
	}
	if len(cr.sessions) != 0 {
		t.Fatalf("expected no reap call for a non-terminal transition, got %v", cr.sessions)
	}
}
