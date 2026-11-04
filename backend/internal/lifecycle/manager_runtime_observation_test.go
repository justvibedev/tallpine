package lifecycle

import (
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestRuntimeObservation_ConfirmedRuntimeDeathTerminates(t *testing.T) {
	m, st, _ := newManager()
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
}

func TestRuntimeObservation_CrashFinalizesUsageBeforeTermination(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	rec.Metadata.RuntimeLaunchID = "launch-1"
	rec.UpdatedAt = time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	st.sessions[rec.ID] = rec
	finalizer := &fakeUsageFinalizer{store: st}
	m.SetUsageFinalizer(finalizer)

	if err := m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{
		Runtime:  ports.ProbeDead,
		Workload: ports.ProbeFailed,
		LaunchID: "launch-1",
	}); err != nil {
		t.Fatal(err)
	}
	if finalizer.calls != 1 || finalizer.sawTerminated {
		t.Fatalf("finalizer calls=%d sawTerminated=%v, want 1/false", finalizer.calls, finalizer.sawTerminated)
	}
	if finalizer.launchID != "launch-1" {
		t.Fatalf("finalizer launch id=%q, want launch-1", finalizer.launchID)
	}
	if finalizer.sessionRevision != rec.Revision {
		t.Fatalf("finalizer session revision=%d, want %d", finalizer.sessionRevision, rec.Revision)
	}
	if !st.sessions[rec.ID].IsTerminated {
		t.Fatal("crashed session was not terminated")
	}
}

func TestRuntimeObservation_FinalizerErrorIsLoggedAndDoesNotBlockTermination(t *testing.T) {
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	st.sessions[rec.ID] = rec
	finalizer := &fakeUsageFinalizer{store: st, err: errors.New("usage unavailable")}
	m.SetUsageFinalizer(finalizer)

	if err := m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{Runtime: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	if !st.sessions[rec.ID].IsTerminated {
		t.Fatal("finalizer error prevented crash termination")
	}
	if got := logs.String(); !strings.Contains(got, "lifecycle: finalize session usage before termination") || !strings.Contains(got, "usage unavailable") {
		t.Fatalf("finalizer error log = %q", got)
	}
}

func TestRuntimeObservation_DoesNotFinalizeRejectedObservations(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	oldActivity := domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-2 * time.Minute)}
	tests := []struct {
		name  string
		rec   domain.SessionRecord
		facts ports.RuntimeFacts
	}{
		{
			name:  "probe failed",
			rec:   domain.SessionRecord{ID: "mer-1", Activity: oldActivity},
			facts: ports.RuntimeFacts{Runtime: ports.ProbeFailed, ObservedAt: now},
		},
		{
			name: "stale launch",
			rec: domain.SessionRecord{
				ID:       "mer-1",
				Activity: oldActivity,
				Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-2"},
			},
			facts: ports.RuntimeFacts{Runtime: ports.ProbeDead, LaunchID: "launch-1", ObservedAt: now},
		},
		{
			name: "workload dead while runtime alive",
			rec: domain.SessionRecord{
				ID:       "mer-1",
				Activity: oldActivity,
				Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
			},
			facts: ports.RuntimeFacts{Runtime: ports.ProbeAlive, Workload: ports.ProbeDead, LaunchID: "launch-1", ObservedAt: now},
		},
		{
			name:  "already terminated",
			rec:   domain.SessionRecord{ID: "mer-1", IsTerminated: true, Activity: domain.Activity{State: domain.ActivityExited}},
			facts: ports.RuntimeFacts{Runtime: ports.ProbeDead, ObservedAt: now},
		},
		{
			name:  "recent activity",
			rec:   domain.SessionRecord{ID: "mer-1", Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}},
			facts: ports.RuntimeFacts{Runtime: ports.ProbeDead, ObservedAt: now},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, st, _ := newManager()
			m.clock = func() time.Time { return now }
			st.sessions[tt.rec.ID] = tt.rec
			finalizer := &fakeUsageFinalizer{store: st}
			m.SetUsageFinalizer(finalizer)

			if err := m.ApplyRuntimeObservation(ctx, tt.rec.ID, tt.facts); err != nil {
				t.Fatal(err)
			}
			if finalizer.calls != 0 {
				t.Fatalf("finalizer calls=%d, want 0", finalizer.calls)
			}
		})
	}
}

func TestRuntimeObservation_DoesNotTerminateNewRuntimeGenerationAfterFinalization(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	rec.Metadata.RuntimeLaunchID = "launch-old"
	st.sessions[rec.ID] = rec
	finalizer := &fakeUsageFinalizer{store: st}
	finalizer.onFinalize = func(id domain.SessionID, _ string, _ int64) error {
		return m.MarkSpawned(ctx, id, domain.SessionMetadata{RuntimeLaunchID: "launch-new"})
	}
	m.SetUsageFinalizer(finalizer)

	done := make(chan error, 1)
	go func() {
		done <- m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{
			Runtime:  ports.ProbeDead,
			LaunchID: "launch-old",
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ApplyRuntimeObservation deadlocked while finalizing usage")
	}

	got := st.sessions[rec.ID]
	if got.IsTerminated || got.Metadata.RuntimeLaunchID != "launch-new" {
		t.Fatalf("stale runtime observation changed new generation: %+v", got)
	}
}

func TestRuntimeObservation_DoesNotTerminateAfterActivityDuringFinalization(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	m, st, _ := newManager()
	m.clock = func() time.Time { return now }
	rec := domain.SessionRecord{
		ID:       "mer-1",
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-2 * time.Minute)},
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	st.sessions[rec.ID] = rec
	finalizer := &fakeUsageFinalizer{store: st}
	finalizer.onFinalize = func(id domain.SessionID, _ string, _ int64) error {
		return m.ApplyActivitySignal(ctx, id, ports.ActivitySignal{
			Valid:     true,
			State:     domain.ActivityIdle,
			Timestamp: now,
			LaunchID:  "launch-1",
		})
	}
	m.SetUsageFinalizer(finalizer)

	if err := m.ApplyRuntimeObservation(ctx, rec.ID, ports.RuntimeFacts{
		Runtime:    ports.ProbeDead,
		LaunchID:   "launch-1",
		ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions[rec.ID]
	if got.IsTerminated || !got.Activity.LastActivityAt.Equal(now) {
		t.Fatalf("runtime observation overrode activity recorded during finalization: %+v", got)
	}
}

func TestRuntimeObservation_RetriesAfterRevisionChangesDuringFinalization(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	m, st, _ := newManager()
	m.clock = func() time.Time { return now }
	rec := domain.SessionRecord{
		ID:        "mer-1",
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-2 * time.Minute)},
		UpdatedAt: now.Add(-2 * time.Minute),
		Metadata:  domain.SessionMetadata{RuntimeLaunchID: "launch-1"},
	}
	st.sessions[rec.ID] = rec
	finalized := 0
	var revisions []int64
	finalizer := &fakeUsageFinalizer{store: st}
	finalizer.onFinalize = func(id domain.SessionID, launchID string, sessionRevision int64) error {
		revisions = append(revisions, sessionRevision)
		if finalizer.calls == 1 {
			if err := m.ApplyActivitySignal(ctx, id, ports.ActivitySignal{
				Valid:     true,
				State:     domain.ActivityExited,
				Timestamp: now,
				Event:     "process-exited",
				LaunchID:  launchID,
			}); err != nil {
				return err
			}
		}
		current := st.sessions[id]
		if !current.IsTerminated &&
			current.Metadata.RuntimeLaunchID == launchID &&
			current.Revision == sessionRevision {
			finalized++
		}
		return nil
	}
	m.SetUsageFinalizer(finalizer)
	facts := ports.RuntimeFacts{
		Runtime:    ports.ProbeDead,
		LaunchID:   "launch-1",
		ObservedAt: now,
	}

	if err := m.ApplyRuntimeObservation(ctx, rec.ID, facts); err != nil {
		t.Fatal(err)
	}
	got := st.sessions[rec.ID]
	if finalized != 0 || got.IsTerminated || got.Activity.State != domain.ActivityExited || !got.UpdatedAt.Equal(now) {
		t.Fatalf("first pass finalized=%d session=%+v, want no finalization and live exited revision", finalized, got)
	}

	if err := m.ApplyRuntimeObservation(ctx, rec.ID, facts); err != nil {
		t.Fatal(err)
	}
	got = st.sessions[rec.ID]
	if finalizer.calls != 2 || finalized != 1 || !got.IsTerminated {
		t.Fatalf("second pass finalizer calls=%d finalized=%d session=%+v", finalizer.calls, finalized, got)
	}
	if len(revisions) != 2 || revisions[0] != rec.Revision || revisions[1] != rec.Revision+1 {
		t.Fatalf("finalizer revisions=%v, want [%d %d]", revisions, rec.Revision, rec.Revision+1)
	}
}

func TestRuntimeObservation_ConfirmedDeathIsSuppressedDuringSessionMutation(t *testing.T) {
	m, st, _ := newManager()
	m.SetSessionOperationGate(fixedSessionOperationGate(true))
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	st.sessions["mer-1"] = rec

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"]; !reflect.DeepEqual(got, rec) {
		t.Fatalf("runtime observation mutated session during exclusive operation: got %+v, want %+v", got, rec)
	}
}

func TestRuntimeObservation_FailedProbeDoesNotMutate(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	before := st.sessions["mer-1"]
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeFailed, Workload: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatalf("failed probe should not persist a state, got %+v", st.sessions["mer-1"])
	}
}

func TestRuntimeObservation_ExitedWorkloadKeepsSessionLive(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-1"
	st.sessions["mer-1"] = rec
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeAlive, Workload: ports.ProbeDead, LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("want live/exited, got %+v", got)
	}
}

func TestRuntimeObservation_AliveWorkloadCannotResurrectExitedSession(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.RuntimeLaunchID = "launch-1"
	st.sessions["mer-1"] = rec
	before := st.sessions["mer-1"]

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{
		Runtime:  ports.ProbeAlive,
		Workload: ports.ProbeAlive,
		LaunchID: "launch-1",
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatalf("original supervisor observation resurrected exited session: %+v", st.sessions["mer-1"])
	}
}

// ExitAgent destroys a terminal on purpose and keeps the session; Resume
// builds a new one. That runtime's death must leave the session live. A release
// names one launch only: a later launch that dies after its agent exited is a
// crash like any other (the exit hook can win the race with the reaper).
func TestRuntimeObservation_ReleasedRuntimeDeathKeepsSessionLive(t *testing.T) {
	for _, tc := range []struct {
		name      string
		released  string
		terminate bool
	}{
		{name: "released launch", released: "launch-1", terminate: false},
		{name: "earlier launch released", released: "launch-0", terminate: true},
		{name: "nothing released", released: "", terminate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _ := newManager()
			rec := working("mer-1")
			rec.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now().Add(-2 * time.Minute)}
			rec.Metadata.RuntimeLaunchID = "launch-1"
			rec.Metadata.RuntimeReleasedLaunchID = tc.released
			st.sessions["mer-1"] = rec

			if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{
				Runtime:  ports.ProbeDead,
				Workload: ports.ProbeFailed,
				LaunchID: "launch-1",
			}); err != nil {
				t.Fatal(err)
			}
			if got := st.sessions["mer-1"]; got.IsTerminated != tc.terminate {
				t.Fatalf("terminated = %v, want %v", got.IsTerminated, tc.terminate)
			}
		})
	}
}

func TestRuntimeObservation_StaleLaunchIsIgnored(t *testing.T) {
	m, st, _ := newManager()
	rec := working("mer-1")
	rec.Metadata.RuntimeLaunchID = "launch-2"
	st.sessions["mer-1"] = rec
	before := st.sessions["mer-1"]
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeAlive, Workload: ports.ProbeDead, LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(st.sessions["mer-1"], before) {
		t.Fatalf("stale launch observation mutated session: %+v", st.sessions["mer-1"])
	}
}
