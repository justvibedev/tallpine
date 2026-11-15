package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// memResearchStore is the persisted research mode a daemon restart reads back.
type memResearchStore struct {
	mu   sync.Mutex
	rows map[domain.SessionID]domain.SessionResearch
}

func (m *memResearchStore) SessionResearch(_ context.Context, id domain.SessionID) (domain.SessionResearch, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	return r, ok, nil
}

func (m *memResearchStore) SetSessionResearch(_ context.Context, id domain.SessionID, r *domain.SessionResearch) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rows == nil {
		m.rows = map[domain.SessionID]domain.SessionResearch{}
	}
	if r == nil {
		delete(m.rows, id)
	} else {
		m.rows[id] = *r
	}
	return nil
}

func (m *memResearchStore) TouchSessionResearch(_ context.Context, id domain.SessionID, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.rows[id]; ok {
		r.LastTurnAt = at
		m.rows[id] = r
	}
	return nil
}

func (m *memResearchStore) ClearAllSessionResearch(context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.rows)
	return nil
}

func (m *memResearchStore) row(id domain.SessionID) (domain.SessionResearch, bool) {
	r, ok, _ := m.SessionResearch(context.Background(), id)
	return r, ok
}

// persistedFixture is a research fixture with a persisted store and a clock.
func persistedFixture(store *memResearchStore, now *time.Time) *researchFixture {
	f := newResearchFixture("")
	f.s.researchStore = store
	f.s.now = func() time.Time { return *now }
	return f
}

func TestResearchWindowSizes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		window string
		call   int // the live window asked for; 0 is the agent's own default
	}{{"250k", 250_000}, {"400k", 400_000}, {"1m", 0}} {
		store := &memResearchStore{}
		now := time.Now()
		f := persistedFixture(store, &now)
		st, err := f.s.SetResearchWindow(ctx, "s1", tc.window)
		if err != nil || !st.Active || st.ResearchWindow != tc.window || st.Window != tc.call {
			t.Fatalf("%s: status %+v err %v", tc.window, st, err)
		}
		if len(f.conv.calls) != 1 || f.conv.calls[0] != tc.call || len(f.lines) != 1 {
			t.Fatalf("%s: calls %v lines %v", tc.window, f.conv.calls, f.lines)
		}
		if row, ok := store.row("s1"); !ok || row.Window != tc.window {
			t.Fatalf("%s: persisted %+v %v", tc.window, row, ok)
		}
	}
}

func TestResearchWindowMovesARunningResearchMode(t *testing.T) {
	ctx := context.Background()
	f := newResearchFixture("")
	if _, err := f.s.SetResearchMode(ctx, "s1", true); err != nil {
		t.Fatal(err)
	}
	st, err := f.s.SetResearchWindow(ctx, "s1", "400k")
	if err != nil || !st.Active || st.Window != 400_000 {
		t.Fatalf("status %+v err %v", st, err)
	}
	// research on without --window keeps the chosen window.
	if st, _ = f.s.SetResearchMode(ctx, "s1", true); st.ResearchWindow != "400k" || len(f.conv.calls) != 2 || len(f.lines) != 2 {
		t.Fatalf("status %+v calls %v lines %v", st, f.conv.calls, f.lines)
	}
}

func TestResearchWindowRefusesASizeTheModelCannotHold(t *testing.T) {
	ctx := context.Background()
	store := &memResearchStore{}
	now := time.Now()
	f := persistedFixture(store, &now)
	f.s.modelCatalog = windowCatalog{"opus": 200_000}
	for _, w := range []string{"250k", "400k", "1m"} {
		if _, err := f.s.SetResearchWindow(ctx, "s1", w); !errors.Is(err, ErrResearchWindowTooLarge) {
			t.Fatalf("%s on a 200k model: err %v, want ErrResearchWindowTooLarge", w, err)
		}
	}
	if _, err := f.s.SetResearchWindow(ctx, "s1", "300k"); !errors.Is(err, ErrResearchWindowInvalid) {
		t.Fatalf("300k: err %v, want ErrResearchWindowInvalid", err)
	}
	if f.c.researchActive() || len(f.conv.calls) != 0 || len(f.lines) != 0 {
		t.Fatalf("a refused window changed something: research %v calls %v lines %v", f.c.researchActive(), f.conv.calls, f.lines)
	}
	if _, ok := store.row("s1"); ok {
		t.Fatal("a refused window was persisted")
	}
}

// restartedService is the service a daemon restart builds: nothing in memory,
// the same persisted store.
func restartedService(f *researchFixture, store *memResearchStore, now *time.Time) (*Service, *Controller) {
	s := compactionService(new(string))
	s.projects, s.dynamicContext, s.announce = f.s.projects, f.s.dynamicContext, f.s.announce
	s.researchStore = store
	s.now = func() time.Time { return *now }
	c := claudeController(&liveWindowConv{applied: true}, 250_000, "")
	c.kind, c.conversation.ProjectID = domain.KindOrchestrator, "p"
	s.controllers = map[domain.SessionID]*Controller{"s1": c}
	return s, c
}

func TestResearchSurvivesADaemonRestartAndStillExpires(t *testing.T) {
	ctx := context.Background()
	store := &memResearchStore{}
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	now := t0
	f := persistedFixture(store, &now)
	if _, err := f.s.SetResearchWindow(ctx, "s1", "400k"); err != nil {
		t.Fatal(err)
	}
	now = t0.Add(10 * time.Minute)
	f.s.researchTurnEnded(ctx, f.c, "turn-1", "")

	// The daemon restarts 20 minutes in: the launch uses the persisted window
	// and the new controller has the flag, since and last turn back.
	now = t0.Add(20 * time.Minute)
	s, c := restartedService(f, store, &now)
	cfg := StartConfig{SessionID: "s1", ProjectID: "p", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Model: "opus", Env: map[string]string{}}
	conv := domain.ConversationRecord{Settings: domain.ConversationSettings{Model: "opus"}}
	s.applyLaunchCompaction(ctx, &cfg, &conv)
	if got := cfg.Env[contextwindow.AutoCompactEnvKey(domain.HarnessClaudeCode)]; got != "400000" {
		t.Fatalf("launch env %q, want the persisted 400k", got)
	}
	s.carryResearchToController(ctx, c)
	if !c.researchActive() || c.research.window != "400k" || !c.research.since.Equal(t0) || !c.researchLastTurn().Equal(t0.Add(10*time.Minute)) {
		t.Fatalf("restored research on=%v window=%q since=%v lastTurn=%v", c.researchActive(), c.research.window, c.research.since, c.researchLastTurn())
	}
	if got := s.desiredCompactTokens(ctx, c); got != 400_000 {
		t.Fatalf("restored controller wants %d, want 400000", got)
	}

	// Another restart an hour after the last research turn: expired, cleared.
	now = t0.Add(10*time.Minute + domain.ResearchQuietExpiry)
	s, c = restartedService(f, store, &now)
	s.carryResearchToController(ctx, c)
	if c.researchActive() {
		t.Fatal("research mode restored after its quiet hour")
	}
	if _, ok := store.row("s1"); ok {
		t.Fatal("expired research mode still persisted")
	}
}

func TestEveryEndOfResearchClearsThePersistedState(t *testing.T) {
	ctx := context.Background()
	for name, end := range map[string]func(f *researchFixture) bool{
		"off": func(f *researchFixture) bool {
			st, err := f.s.SetResearchMode(ctx, "s1", false)
			return err == nil && !st.Active
		},
		// The sweeper calls ClearResearch before an idle compaction or a handoff.
		"idle compaction or handoff": func(f *researchFixture) bool { return f.s.ClearResearch(ctx, "s1") },
		"idle compaction of a hibernated orchestrator": func(f *researchFixture) bool {
			delete(f.s.controllers, "s1")
			return f.s.ClearResearch(ctx, "s1")
		},
	} {
		store := &memResearchStore{}
		now := time.Now()
		f := persistedFixture(store, &now)
		if _, err := f.s.SetResearchMode(ctx, "s1", true); err != nil {
			t.Fatal(err)
		}
		if _, ok := store.row("s1"); !ok {
			t.Fatalf("%s: research mode not persisted", name)
		}
		if !end(f) {
			t.Fatalf("%s: the end did not report research mode", name)
		}
		if _, ok := store.row("s1"); ok {
			t.Fatalf("%s: persisted research mode survived", name)
		}
	}
}

// thrashFixture is an orchestrator at the 120k coordination window whose agent
// applies a window only at launch, so a step-up restarts it.
func thrashFixture(t *testing.T) (*researchFixture, *restartRecorder, *memResearchStore) {
	t.Helper()
	f, rec := keptWindowFixture()
	store := &memResearchStore{}
	f.s.researchStore = store
	return f, rec, store
}

// relaunch stands in for the restart: a new controller at the launched window
// takes the carried research mode.
func relaunch(f *researchFixture, live int) *Controller {
	c := claudeController(f.conv, live, "")
	c.kind, c.conversation.ProjectID = domain.KindOrchestrator, "p"
	f.s.mu.Lock()
	f.s.controllers["s1"] = c
	f.s.mu.Unlock()
	f.s.carryResearchToController(context.Background(), c)
	return c
}

func TestThrashStepsTheOrchestratorWindowOneRungPerNotice(t *testing.T) {
	ctx := context.Background()
	f, rec, store := thrashFixture(t)
	if !f.s.stepUpOnThrash(ctx, f.c, "turn-1") {
		t.Fatal("120k: no step")
	}
	if len(f.lines) != 1 || !strings.Contains(f.lines[0], "from 120k tokens to 250k tokens") {
		t.Fatalf("lines = %v, want one step line 120k -> 250k", f.lines)
	}
	// The same notice, or another while the step waits for its restart, does
	// not step again.
	if f.s.stepUpOnThrash(ctx, f.c, "turn-1") || f.s.stepUpOnThrash(ctx, f.c, "turn-2") {
		t.Fatal("stepped twice for one notice")
	}
	waitFor(t, "restart and wake", func() bool { n, m := rec.snapshot(); return n == 1 && len(m) == 1 })
	if _, m := rec.snapshot(); !strings.Contains(m[0], "thrashing") || !strings.Contains(m[0], "250k tokens") {
		t.Fatalf("wake message %q", m[0])
	}
	if row, ok := store.row("s1"); !ok || row.Window != "250k" {
		t.Fatalf("persisted %+v %v", row, ok)
	}

	c := relaunch(f, 250_000)
	if !f.s.stepUpOnThrash(ctx, c, "turn-3") {
		t.Fatal("250k: no step")
	}
	if len(f.lines) != 2 || !strings.Contains(f.lines[1], "from 250k tokens to 400k tokens") {
		t.Fatalf("lines = %v, want a second step line 250k -> 400k", f.lines)
	}
	if row, _ := store.row("s1"); row.Window != "400k" {
		t.Fatalf("persisted window %q, want 400k", row.Window)
	}
}

func TestThrashDoesNotStepAtTheTopOrPastTheModel(t *testing.T) {
	ctx := context.Background()
	f, _, _ := thrashFixture(t)
	f.c.compactLive = 0 // 1m: the agent's own default on a 1M model
	if f.s.stepUpOnThrash(ctx, f.c, "turn-1") {
		t.Fatal("stepped at 1m")
	}
	f, rec, _ := thrashFixture(t)
	f.s.modelCatalog = windowCatalog{"opus": 200_000}
	if f.s.stepUpOnThrash(ctx, f.c, "turn-1") {
		t.Fatal("stepped to a rung a 200k model cannot hold")
	}
	f, _, _ = thrashFixture(t)
	f.on = false // research mode turned off in Settings
	if f.s.stepUpOnThrash(ctx, f.c, "turn-1") {
		t.Fatal("stepped with research mode turned off")
	}
	if len(f.lines) != 0 || f.c.researchActive() {
		t.Fatalf("lines %v research %v", f.lines, f.c.researchActive())
	}
	time.Sleep(20 * time.Millisecond)
	if n, _ := rec.snapshot(); n != 0 {
		t.Fatalf("restarted %d times without a step", n)
	}
}

func TestTurnEndStepsUpOnThrashAndMovesTheQuietClock(t *testing.T) {
	ctx := context.Background()
	f, _, store := thrashFixture(t)
	now := time.Now().UTC()
	f.s.now = func() time.Time { return now }
	f.s.researchTurnEnded(ctx, f.c, "turn-1", "Autocompact is thrashing: ...")
	waitFor(t, "thrash step", func() bool { _, ok := store.row("s1"); return ok })
	now = now.Add(5 * time.Minute)
	f.s.researchTurnEnded(ctx, f.c, "turn-2", "")
	if row, _ := store.row("s1"); !row.LastTurnAt.Equal(now) || !f.c.researchLastTurn().Equal(now) {
		t.Fatalf("last turn %v / %v, want %v", row.LastTurnAt, f.c.researchLastTurn(), now)
	}
}

// researchSessions reads the session record of an orchestrator without a live
// chat controller.
type researchSessions domain.SessionRecord

func (r researchSessions) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord(r), true, nil
}

func TestTerminalAndNonClaudeOrchestratorsAreUnsupported(t *testing.T) {
	ctx := context.Background()
	// A Codex chat orchestrator: refused, and a thrash stop does not step.
	f := newResearchFixture("")
	f.c.harness = domain.HarnessCodex
	if _, err := f.s.SetResearchMode(ctx, "s1", true); !errors.Is(err, ErrResearchUnsupported) {
		t.Fatalf("codex on: err %v", err)
	}
	if _, err := f.s.SetResearchWindow(ctx, "s1", "400k"); !errors.Is(err, ErrResearchUnsupported) {
		t.Fatalf("codex window: err %v", err)
	}
	if f.s.stepUpOnThrash(ctx, f.c, "turn-1") || len(f.lines) != 0 || len(f.conv.calls) != 0 {
		t.Fatalf("codex stepped: lines %v calls %v", f.lines, f.conv.calls)
	}
	// Terminal-mode orchestrators have no chat controller at all.
	for name, rec := range map[string]domain.SessionRecord{
		"terminal claude": {Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI},
		"terminal codex":  {Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI},
	} {
		s := compactionService(new(string))
		s.sessions = researchSessions(rec)
		if _, err := s.SetResearchMode(ctx, "s1", true); !errors.Is(err, ErrResearchUnsupported) {
			t.Fatalf("%s on: err %v", name, err)
		}
		if _, err := s.SetResearchWindow(ctx, "s1", "250k"); !errors.Is(err, ErrResearchUnsupported) {
			t.Fatalf("%s window: err %v", name, err)
		}
	}
	// A hibernated chat-mode Claude Code orchestrator is not "unsupported".
	s := compactionService(new(string))
	s.sessions = researchSessions{Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat}
	if _, err := s.SetResearchMode(ctx, "s1", true); !errors.Is(err, ErrNoController) {
		t.Fatalf("hibernated chat: err %v, want ErrNoController", err)
	}
}

func TestWindowChangeWhileARestartWaitsRestartsOnce(t *testing.T) {
	ctx := context.Background()
	f, rec, _ := thrashFixture(t)
	setBusy(f.c, "turn-1")
	if _, err := f.s.SetResearchMode(ctx, "s1", true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetResearchWindow(ctx, "s1", "400k"); err != nil {
		t.Fatal(err)
	}
	if carried, _ := f.s.researchCarried.get("s1"); carried.Window != "400k" {
		t.Fatalf("carried window %q, want 400k", carried.Window)
	}
	setBusy(f.c, "")
	waitFor(t, "restart and wake", func() bool { n, m := rec.snapshot(); return n == 1 && len(m) == 1 })
	time.Sleep(30 * time.Millisecond)
	if n, m := rec.snapshot(); n != 1 || len(m) != 1 {
		t.Fatalf("restarts %d messages %v, want one each", n, m)
	}
}

// Review of #1220: the global toggle turning off also ends the research mode of
// an orchestrator with no controller, so turning it back on does not revive it.
func TestGlobalToggleOffClearsAHibernatedOrchestratorsResearch(t *testing.T) {
	ctx := context.Background()
	store := &memResearchStore{}
	now := time.Now()
	f := persistedFixture(store, &now)
	if _, err := f.s.SetResearchWindow(ctx, "s1", "400k"); err != nil {
		t.Fatal(err)
	}
	delete(f.s.controllers, "s1") // hibernated
	f.s.researchCarried.put("s1", carriedResearch{SessionResearch: domain.SessionResearch{Window: "400k", Since: now}})
	f.on = false
	f.s.ClearAllResearch(ctx)
	if _, ok := store.row("s1"); ok || f.s.researchCarried.has("s1") {
		t.Fatalf("persisted %v carried %v after the toggle went off", ok, f.s.researchCarried.has("s1"))
	}
	f.on = true
	c := relaunch(f, 120_000) // woken after the toggle is back on
	if c.researchActive() {
		t.Fatal("research mode came back after the toggle was turned off and on")
	}
}
