package sessionmanager

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func TestInterfaceTargetUsesReservedFallbackOwner(t *testing.T) {
	ctx := context.Background()
	m, fake, _, chat, _ := newTransitionManager(t, domain.SessionModeTUI)
	st := sqlitetest.MustOpenAt(t, t.TempDir())
	now := time.Now().UTC()
	project := fake.projects["proj"]
	project.Path = t.TempDir()
	project.RegisteredAt = now
	if err := st.UpsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	rec := fake.sessions["session-1"]
	rec.ID = ""
	rec.CreatedAt = now
	rec.UpdatedAt = now
	rec.Harness = domain.HarnessCodex
	rec.Metadata.WorkspacePath = project.Path
	var err error
	rec, err = st.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	lcm := lifecycle.New(st, nil)
	for _, event := range []string{"user-prompt-submit", "stop"} {
		state := domain.ActivityActive
		if event == "stop" {
			state = domain.ActivityIdle
		}
		if err = lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{Valid: true, State: state, Event: event, Timestamp: now,
			LaunchID: rec.Metadata.RuntimeLaunchID, AgentSessionID: "native-1", ProviderTurnID: "native-turn", LatestUserPrompt: "request", FinalAssistantMessage: "final"}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := st.ListPendingTurnFallbacks(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v %v", pending, err)
	}
	original := pending[0]
	_, _, err = st.CreateSessionInterfaceTransition(ctx, domain.SessionInterfaceTransition{ID: "handoff", SessionID: rec.ID,
		SourceMode: domain.SessionModeTUI, TargetMode: domain.SessionModeChat, Policy: domain.SessionInterfaceTransitionDrain,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict, Phase: domain.SessionInterfaceTransitionSourceStopped, NativeConversationID: "native-1", CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := lcm.CommitControllerEpoch(ctx, rec.ID, domain.SessionModeTUI, domain.SessionModeChat, "native-1", false); err != nil || !changed {
		t.Fatalf("epoch=%v %v", changed, err)
	}
	m.store = st
	m.lcm = lcm
	if err = m.startTransitionTarget(ctx, rec.ID, "handoff", false, true, domain.SessionInterfaceTransitionHistoryStrict); err != nil {
		t.Fatal(err)
	}
	current, _, err := st.GetSession(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chat.start.ControllerGeneration == "" || current.Metadata.ControllerGeneration != chat.start.ControllerGeneration {
		t.Fatalf("target ignored reserved claim: cfg=%q committed=%q", chat.start.ControllerGeneration, current.Metadata.ControllerGeneration)
	}
	pending, err = st.ListPendingTurnFallbacks(ctx)
	if err != nil || len(pending) != 1 || pending[0] != original {
		t.Fatalf("target lost final: %+v %v", pending, err)
	}
}
