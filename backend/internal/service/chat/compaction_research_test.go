package chat

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type researchFixture struct {
	s     *Service
	c     *Controller
	conv  *liveWindowConv
	lines []string
	proj  compactProjects
	on    bool
}

func newResearchFixture(own string) *researchFixture {
	def := ""
	f := &researchFixture{s: compactionService(&def), conv: &liveWindowConv{applied: true}, proj: compactProjects{"p": {}}, on: true}
	f.s.projects = f.proj
	f.s.dynamicContext = func(context.Context) bool { return f.on }
	f.s.announce = func(_ *Controller, summary string) { f.lines = append(f.lines, summary) }
	f.c = claudeController(f.conv, 120_000, own)
	f.c.kind, f.c.conversation.ProjectID = domain.KindOrchestrator, "p"
	f.s.controllers = map[domain.SessionID]*Controller{"s1": f.c}
	return f
}

func TestResearchRaisesTheWindowAndOffRestoresIt(t *testing.T) {
	f := newResearchFixture("")
	ctx := context.Background()
	st, err := f.s.SetResearchMode(ctx, "s1", true)
	if err != nil || !st.Active {
		t.Fatalf("on: status %+v err %v", st, err)
	}
	if st, err = f.s.SetResearchMode(ctx, "s1", false); err != nil || st.Active {
		t.Fatalf("off: status %+v err %v", st, err)
	}
	if len(f.conv.calls) != 2 || f.conv.calls[0] != 250_000 || f.conv.calls[1] != 120_000 {
		t.Fatalf("window calls = %v, want [250000 120000]", f.conv.calls)
	}
	if len(f.lines) != 2 {
		t.Fatalf("chat lines = %v, want one per switch", f.lines)
	}
}

func TestResearchOnWhileOnIsIdempotent(t *testing.T) {
	f := newResearchFixture("")
	ctx := context.Background()
	for range 2 {
		if _, err := f.s.SetResearchMode(ctx, "s1", true); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.conv.calls) != 1 || len(f.lines) != 1 {
		t.Fatalf("calls %v lines %v, want one each", f.conv.calls, f.lines)
	}
}

func TestResearchNeverOverridesAChatsOwnWindow(t *testing.T) {
	f := newResearchFixture("200k")
	f.c.compactLive = 200_000
	st, err := f.s.SetResearchMode(context.Background(), "s1", true)
	if err != nil || st.Active || len(f.conv.calls) != 0 {
		t.Fatalf("own window: status %+v err %v calls %v", st, err, f.conv.calls)
	}
}

func TestResearchCompactionFallsBackWhenNotBelowTheModelWindow(t *testing.T) {
	f := newResearchFixture("")
	f.s.modelCatalog = windowCatalog{"opus": 200_000}
	f.proj["p"] = domain.ProjectConfig{Orchestrator: domain.RoleOverride{ResearchCompactWindow: "250k"}}
	f.c.compactLive = 120_000
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	if got := f.s.desiredCompactTokens(context.Background(), f.c); got != 0 {
		t.Fatalf("desired = %d, want 0 (the agent's own default)", got)
	}
}

func TestResearchIsRefusedWhenTurnedOff(t *testing.T) {
	ctx := context.Background()
	f := newResearchFixture("")
	f.on = false
	if _, err := f.s.SetResearchMode(ctx, "s1", true); !errors.Is(err, ErrResearchDisabled) {
		t.Fatalf("global off: err %v", err)
	}
	f = newResearchFixture("")
	f.proj["p"] = domain.ProjectConfig{Orchestrator: domain.RoleOverride{ResearchCompactWindow: domain.AutoCompactOff}}
	if _, err := f.s.SetResearchMode(ctx, "s1", true); !errors.Is(err, ErrResearchDisabled) {
		t.Fatalf("project off: err %v", err)
	}
}

func TestTurningTheGlobalToggleOffClearsRunningResearch(t *testing.T) {
	f := newResearchFixture("")
	ctx := context.Background()
	if _, err := f.s.SetResearchMode(ctx, "s1", true); err != nil {
		t.Fatal(err)
	}
	f.on = false
	f.s.ClearAllResearch(ctx)
	if f.c.researchActive() || f.c.compactLive != 120_000 {
		t.Fatalf("research %v live %d, want cleared at 120k", f.c.researchActive(), f.c.compactLive)
	}
}

func TestResearchRequestErrorIsNotReportedAsAKeptWindow(t *testing.T) {
	f := newResearchFixture("")
	var buf bytes.Buffer
	f.s.log = slog.New(slog.NewTextHandler(&buf, nil))
	f.conv.err = errors.New("ACP timeout")
	_, err := f.s.SetResearchMode(context.Background(), "s1", true)
	if !errors.Is(err, ErrResearchRequestFailed) {
		t.Fatalf("err %v, want ErrResearchRequestFailed", err)
	}
	if f.c.researchActive() {
		t.Fatal("a failed request must clear the flag")
	}
	if st, _ := f.s.ResearchStatus(context.Background(), "s1"); !st.Available {
		t.Fatalf("status %+v: a retry could work, research must stay available", st)
	}
}

func TestResearchIsRefusedForOtherAgentsAndKinds(t *testing.T) {
	ctx := context.Background()
	f := newResearchFixture("")
	f.c.harness = domain.HarnessCodex
	if _, err := f.s.SetResearchMode(ctx, "s1", true); !errors.Is(err, ErrResearchUnsupported) {
		t.Fatalf("codex: err %v", err)
	}
	// A bridge that cannot change its window live still offers research mode:
	// it takes the window through a restart.
	f, rec := keptWindowFixture()
	f.c.conv = plainConv{}
	if st, err := f.s.SetResearchMode(ctx, "s1", true); err != nil || !st.RestartPending {
		t.Fatalf("no live window: status %+v err %v", st, err)
	}
	waitFor(t, "restart", func() bool { n, _ := rec.snapshot(); return n == 1 })
	f = newResearchFixture("")
	f.c.kind = domain.KindWorker
	if _, err := f.s.SetResearchMode(ctx, "s1", true); !errors.Is(err, ErrResearchUnsupported) {
		t.Fatalf("worker: err %v", err)
	}
}

// The idle threshold is sized from the coordination window, so research mode
// leaves it at 30% of 120k.
func TestIdleThresholdIgnoresTheResearchWindow(t *testing.T) {
	f := newResearchFixture("")
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	window := int64(f.s.desiredCompactTokensIn(context.Background(), f.c, false))
	if got := domain.IdleCompactThreshold("", window); got != 36_000 {
		t.Fatalf("idle threshold = %d, want 36000", got)
	}
}

// A chat with its own window is unchanged by research mode, so a switch writes
// no "window raised" chat line.
func TestResearchWritesNoChatLineForAChatWithItsOwnWindow(t *testing.T) {
	f := newResearchFixture("200k")
	f.c.compactLive = 200_000
	ctx := context.Background()
	if _, err := f.s.SetResearchMode(ctx, "s1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetResearchMode(ctx, "s1", false); err != nil {
		t.Fatal(err)
	}
	if len(f.lines) != 0 {
		t.Fatalf("chat lines = %v, want none", f.lines)
	}
}

// Research only raises the window: a coordination window above the research
// window, or the agent's own default, stays, and no "raised" line is written.
func TestResearchNeverLowersTheCoordinationWindow(t *testing.T) {
	for name, window := range map[string]string{"larger project window": "400k", "agent default": domain.AutoCompactOff} {
		f := newResearchFixture("")
		f.proj["p"] = domain.ProjectConfig{Orchestrator: domain.RoleOverride{AutoCompactWindow: window}}
		f.c.compactLive = f.s.desiredCompactTokensIn(context.Background(), f.c, false)
		before := f.c.compactLive
		st, err := f.s.SetResearchMode(context.Background(), "s1", true)
		if err != nil || st.Active || len(f.conv.calls) != 0 || len(f.lines) != 0 || f.c.compactLive != before {
			t.Errorf("%s: status %+v err %v calls %v lines %v live %d (was %d), want no change", name, st, err, f.conv.calls, f.lines, f.c.compactLive, before)
		}
	}
}
