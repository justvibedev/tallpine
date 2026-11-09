package daemon

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type recordingActivity struct{ got []ports.ActivitySignal }

func (r *recordingActivity) ApplyActivitySignal(_ context.Context, _ domain.SessionID, s ports.ActivitySignal) error {
	r.got = append(r.got, s)
	return nil
}

// B622: the provider host dying with the app must not be stored as the agent
// exiting, or the next boot treats the worker as stopped on purpose.
func TestShutdownActivityFilterDropsOnlyUnintentionalStopsDuringShutdown(t *testing.T) {
	stoppedSignal := ports.ActivitySignal{State: domain.ActivityExited, Event: "chat.controller.stopped"}
	intentional := stoppedSignal
	intentional.IntentionalStop = true
	active := ports.ActivitySignal{State: domain.ActivityActive, Event: "chat.turn.started"}
	// What a persistent host's socket dropping produces: the real B622 path.
	disconnected := ports.ActivitySignal{State: domain.ActivityExited, Event: "chat.controller.disconnected"}
	intentionalDisconnect := disconnected
	intentionalDisconnect.IntentionalStop = true

	for name, tc := range map[string]struct {
		stopping bool
		signal   ports.ActivitySignal
		wantKept bool
	}{
		"host lost during shutdown is dropped": {true, stoppedSignal, false},
		"host lost while running is recorded":  {false, stoppedSignal, true},
		"daemon-owned stop still recorded":     {true, intentional, true},
		"other signals during shutdown pass":   {true, active, true},

		"persistent host lost during shutdown is dropped": {true, disconnected, false},
		"persistent host lost while running is recorded":  {false, disconnected, true},
		"daemon-owned disconnect still recorded":          {true, intentionalDisconnect, true},
	} {
		t.Run(name, func(t *testing.T) {
			rec := &recordingActivity{}
			f := shutdownActivityFilter{ActivityRecorder: rec, stopping: func() bool { return tc.stopping }}
			if err := f.ApplyActivitySignal(context.Background(), "w-1", tc.signal); err != nil {
				t.Fatal(err)
			}
			if kept := len(rec.got) == 1; kept != tc.wantKept {
				t.Fatalf("kept = %v, want %v", kept, tc.wantKept)
			}
		})
	}
}

type lossStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	reasons  map[domain.SessionID]domain.AgentExitReason
}

func (s *lossStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

func (s *lossStore) SetSessionAgentExitReason(_ context.Context, id domain.SessionID, r domain.AgentExitReason) error {
	s.reasons[id] = r
	return nil
}

// ApplyActivitySignal stands in for the recorder behind the filter: like the
// real one it stores the exit, so settle sees what a real shutdown would.
func (s *lossStore) ApplyActivitySignal(_ context.Context, id domain.SessionID, sig ports.ActivitySignal) error {
	rec := s.sessions[id]
	rec.Activity.State = sig.State
	s.sessions[id] = rec
	return nil
}

func withState(state domain.ActivityState) domain.SessionRecord {
	var rec domain.SessionRecord
	rec.Activity.State = state
	return rec
}

// B627: a Force Quit reaches the daemon as supervisor EOF, so b.ctx is live for
// the grace period and the drain. A host lost in that window must still be seen
// as lost to the shutdown, and a mid-turn one must reach the restart tracker.
func TestHostLossBeforeShutdownIsLostHostAndReachesTracker(t *testing.T) {
	const window = 20 * time.Second
	base := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	disconnected := ports.ActivitySignal{State: domain.ActivityExited, Event: "chat.controller.disconnected"}
	stopped := ports.ActivitySignal{State: domain.ActivityExited, Event: "chat.controller.stopped"}

	for name, tc := range map[string]struct {
		before     domain.ActivityState
		signal     ports.ActivitySignal
		age        time.Duration
		restarted  bool
		wantLost   bool
		wantCutOff bool
	}{
		"mid-turn host lost in the window":  {before: domain.ActivityActive, signal: disconnected, age: 5 * time.Second, wantLost: true, wantCutOff: true},
		"mid-turn controller stop":          {before: domain.ActivityActive, signal: stopped, age: time.Second, wantLost: true, wantCutOff: true},
		"idle host lost is lost, no cutoff": {before: domain.ActivityIdle, signal: disconnected, age: 5 * time.Second, wantLost: true},
		"older than the window is a crash":  {before: domain.ActivityActive, signal: disconnected, age: 21 * time.Second},
		"agent came back since":             {before: domain.ActivityActive, signal: disconnected, age: time.Second, restarted: true},
		"daemon-owned stop is not a loss": {before: domain.ActivityActive, age: time.Second,
			signal: ports.ActivitySignal{State: domain.ActivityExited, Event: "chat.controller.stopped", IntentionalStop: true}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st := &lossStore{sessions: map[domain.SessionID]domain.SessionRecord{"w-1": withState(tc.before)}, reasons: map[domain.SessionID]domain.AgentExitReason{}}
			clock := base
			ledger := newHostLossLedger(st, window)
			ledger.now = func() time.Time { return clock }
			f := shutdownActivityFilter{ActivityRecorder: st, stopping: func() bool { return false }, losses: ledger}
			if err := f.ApplyActivitySignal(ctx, "w-1", tc.signal); err != nil {
				t.Fatal(err)
			}
			if tc.restarted {
				st.sessions["w-1"] = withState(domain.ActivityIdle)
			}
			clock = base.Add(tc.age)
			var cutOff []domain.SessionID
			var at time.Time
			ledger.settle(ctx, func(id domain.SessionID, when time.Time) { cutOff, at = append(cutOff, id), when })

			if lost := st.reasons["w-1"] == domain.AgentExitHostLost; lost != tc.wantLost {
				t.Fatalf("marked host_lost = %v, want %v", lost, tc.wantLost)
			}
			if got := len(cutOff) == 1; got != tc.wantCutOff {
				t.Fatalf("cut off = %v, want %v", cutOff, tc.wantCutOff)
			}
			if tc.wantCutOff && !at.Equal(base) {
				t.Fatalf("cut off at %v, want the moment of the loss %v", at, base)
			}
		})
	}
}

// A repeat exit event must not erase that the session was mid-turn.
func TestHostLossRepeatKeepsMidTurn(t *testing.T) {
	ctx := context.Background()
	st := &lossStore{sessions: map[domain.SessionID]domain.SessionRecord{"w-1": withState(domain.ActivityActive)}, reasons: map[domain.SessionID]domain.AgentExitReason{}}
	ledger := newHostLossLedger(st, time.Minute)
	f := shutdownActivityFilter{ActivityRecorder: st, stopping: func() bool { return false }, losses: ledger}
	disconnected := ports.ActivitySignal{State: domain.ActivityExited, Event: "chat.controller.disconnected"}
	for range 2 {
		if err := f.ApplyActivitySignal(ctx, "w-1", disconnected); err != nil {
			t.Fatal(err)
		}
	}
	var cutOff int
	ledger.settle(ctx, func(domain.SessionID, time.Time) { cutOff++ })
	if cutOff != 1 {
		t.Fatalf("cut off %d times, want 1", cutOff)
	}
}
