package lifecycle

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// B545: a terminal orchestrator's turn end releases reports waiting on it. Only
// that session qualifies: chat orchestrators signal from their own controller.
func TestOrchestratorIdleObserverFiresOnTerminalOrchestratorTurnEnd(t *testing.T) {
	cases := []struct {
		name string
		kind domain.SessionKind
		mode domain.SessionMode
		want int
	}{
		{"terminal orchestrator", domain.KindOrchestrator, domain.SessionModeTUI, 1},
		{"chat orchestrator", domain.KindOrchestrator, domain.SessionModeChat, 0},
		{"terminal worker", domain.KindWorker, domain.SessionModeTUI, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _ := newManager()
			rec := working("orc-1")
			rec.Kind, rec.Mode = tc.kind, tc.mode
			rec.Activity.State = domain.ActivityActive
			rec.Metadata.ControllerGeneration = "gen-1"
			st.sessions["orc-1"] = rec
			var got []domain.SessionRecord
			m.SetOrchestratorIdleObserver(func(r domain.SessionRecord) { got = append(got, r) })

			signal := ports.ActivitySignal{Valid: true, State: domain.ActivityIdle, Event: "stop"}
			if tc.mode == domain.SessionModeChat {
				signal.Event, signal.ControllerGeneration = "chat.turn.completed", "gen-1"
			}
			if err := m.ApplyActivitySignal(ctx, "orc-1", signal); err != nil {
				t.Fatal(err)
			}
			if st.sessions["orc-1"].Activity.State != domain.ActivityIdle {
				t.Fatalf("signal was not applied: %+v", st.sessions["orc-1"].Activity)
			}
			if len(got) != tc.want {
				t.Fatalf("observer calls = %d, want %d", len(got), tc.want)
			}
			if tc.want == 1 && (got[0].ID != "orc-1" || got[0].Metadata.ControllerGeneration != "gen-1") {
				t.Fatalf("observer got %+v", got[0])
			}
		})
	}
}
