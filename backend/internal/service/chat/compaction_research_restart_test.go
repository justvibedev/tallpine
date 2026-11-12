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

type restartRecorder struct {
	mu       sync.Mutex
	restarts int
	messages []string
	err      error
}

func (r *restartRecorder) restart(context.Context, domain.SessionID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restarts++
	return r.err
}

func (r *restartRecorder) send(_ context.Context, _ domain.SessionID, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, text)
	return nil
}

func (r *restartRecorder) snapshot() (int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restarts, append([]string(nil), r.messages...)
}

// keptWindowFixture is a research fixture whose agent keeps its window, so
// switching research on needs a restart.
func keptWindowFixture() (*researchFixture, *restartRecorder) {
	f := newResearchFixture("")
	f.conv.applied = false
	rec := &restartRecorder{}
	f.s.researchPoll = 2 * time.Millisecond
	f.s.researchRestartFn, f.s.researchSendFn = rec.restart, rec.send
	return f, rec
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func setBusy(c *Controller, turn string) {
	c.mu.Lock()
	c.pendingTurnID = turn
	c.mu.Unlock()
}

func TestResearchOnRestartsTheIdleAgentAndWakesIt(t *testing.T) {
	f, rec := keptWindowFixture()
	setBusy(f.c, "turn-1") // the orchestrator asks for research mid-turn
	st, err := f.s.SetResearchMode(context.Background(), "s1", true)
	if err != nil || !st.RestartPending || st.Active || !st.Available {
		t.Fatalf("status %+v err %v, want pending and not yet active", st, err)
	}
	if !f.s.researchCarried.has("s1") || len(f.lines) != 1 || !strings.Contains(f.lines[0], "restarts when idle") {
		t.Fatalf("carried %v lines %v", f.s.researchCarried.has("s1"), f.lines)
	}
	time.Sleep(30 * time.Millisecond)
	if n, _ := rec.snapshot(); n != 0 {
		t.Fatalf("restarted %d times while the agent was mid-turn", n)
	}
	setBusy(f.c, "")
	waitFor(t, "restart and wake message", func() bool { n, m := rec.snapshot(); return n == 1 && len(m) == 1 })
	if _, m := rec.snapshot(); !strings.Contains(m[0], "Continue the reading task") || !strings.Contains(m[0], "250k") {
		t.Fatalf("wake message %q", m[0])
	}
}

func TestResearchOffCancelsAPendingRestart(t *testing.T) {
	f, rec := keptWindowFixture()
	setBusy(f.c, "turn-1")
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	st, err := f.s.SetResearchMode(context.Background(), "s1", false)
	if err != nil || st.RestartPending || f.s.researchCarried.has("s1") || f.c.researchActive() {
		t.Fatalf("status %+v err %v carried %v", st, err, f.s.researchCarried.has("s1"))
	}
	setBusy(f.c, "")
	time.Sleep(40 * time.Millisecond)
	if n, m := rec.snapshot(); n != 0 || len(m) != 0 {
		t.Fatalf("restarts %d messages %v after research was switched off", n, m)
	}
}

func TestIdleCompactionCancelsAPendingRestart(t *testing.T) {
	f, rec := keptWindowFixture()
	setBusy(f.c, "turn-1")
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	if !f.s.ClearResearch(context.Background(), "s1") {
		t.Fatal("ClearResearch reported research was not on")
	}
	setBusy(f.c, "")
	time.Sleep(40 * time.Millisecond)
	if n, _ := rec.snapshot(); n != 0 || f.s.researchCarried.has("s1") {
		t.Fatalf("restarts %d carried %v", n, f.s.researchCarried.has("s1"))
	}
}

func TestFailedResearchRestartLeavesCoordinationAndSaysSo(t *testing.T) {
	f, rec := keptWindowFixture()
	rec.err = errors.New("provider would not start")
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "failure message", func() bool { _, m := rec.snapshot(); return len(m) == 1 })
	if _, m := rec.snapshot(); !strings.Contains(m[0], "could not be enabled") {
		t.Fatalf("message %q", m[0])
	}
	if f.c.researchActive() || f.s.researchCarried.has("s1") {
		t.Fatal("a failed restart must clear the research flag and the carried flag")
	}
	if last := f.lines[len(f.lines)-1]; !strings.Contains(last, "not enabled") {
		t.Fatalf("chat lines %v", f.lines)
	}
}

func TestHibernatedAgentIsWokenWithTheCarriedFlag(t *testing.T) {
	f, rec := keptWindowFixture()
	setBusy(f.c, "turn-1")
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	f.s.researchHibernatedFn = func(context.Context, domain.SessionID) bool { return true }
	// Hibernation dropped the controller; no restart is needed, the wake is the launch.
	f.s.mu.Lock()
	delete(f.s.controllers, "s1")
	f.s.mu.Unlock()
	waitFor(t, "wake message", func() bool { _, m := rec.snapshot(); return len(m) == 1 })
	if n, _ := rec.snapshot(); n != 0 || !f.s.researchCarried.has("s1") {
		t.Fatalf("restarts %d carried %v, want a wake only with the flag kept", n, f.s.researchCarried.has("s1"))
	}
}

func launchFixture(model string) (*Service, StartConfig, domain.ConversationRecord) {
	f := newResearchFixture("")
	cfg := StartConfig{SessionID: "s1", ProjectID: "p", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, Model: model, Env: map[string]string{}}
	return f.s, cfg, domain.ConversationRecord{Settings: domain.ConversationSettings{Model: model}}
}

func TestLaunchUsesTheCarriedResearchWindowOnce(t *testing.T) {
	for _, model := range []string{"opus", "model-missing-from-the-account-list"} {
		s, cfg, conv := launchFixture(model)
		key := contextwindow.AutoCompactEnvKey(domain.HarnessClaudeCode)
		coordination := cfg
		s.applyLaunchCompaction(context.Background(), &coordination, &conv)
		s.researchCarried.put("s1", time.Now())
		s.applyLaunchCompaction(context.Background(), &cfg, &conv)
		if cfg.Env[key] != "250000" || coordination.Env[key] == "250000" {
			t.Fatalf("%s: research env %q, coordination env %q", model, cfg.Env[key], coordination.Env[key])
		}
		c := claudeController(nil, 250_000, "")
		c.kind, c.conversation.ProjectID = domain.KindOrchestrator, "p"
		s.carryResearchToController(context.Background(), c)
		if !c.researchActive() || s.researchCarried.has("s1") {
			t.Fatalf("%s: controller research %v, carried %v", model, c.researchActive(), s.researchCarried.has("s1"))
		}
		if s.desiredCompactTokens(context.Background(), c) != 250_000 {
			t.Fatalf("%s: the new controller wants %d", model, s.desiredCompactTokens(context.Background(), c))
		}
	}
}

func TestLaunchKeepsAChatsOwnWindowOverCarriedResearch(t *testing.T) {
	s, cfg, conv := launchFixture("opus")
	conv.Settings.AutoCompactWindow = "300k"
	s.researchCarried.put("s1", time.Now())
	s.applyLaunchCompaction(context.Background(), &cfg, &conv)
	if got := cfg.Env[contextwindow.AutoCompactEnvKey(domain.HarnessClaudeCode)]; got != "300000" {
		t.Fatalf("env %q, want the chat's own 300k", got)
	}
}

func TestStoppedChatIsNotWokenForPendingResearch(t *testing.T) {
	f, rec := keptWindowFixture()
	f.s.researchHibernatedFn = func(context.Context, domain.SessionID) bool { return false }
	setBusy(f.c, "turn-1")
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	f.s.mu.Lock()
	delete(f.s.controllers, "s1")
	f.s.mu.Unlock()
	waitFor(t, "carry dropped", func() bool { return !f.s.researchCarried.has("s1") })
	time.Sleep(20 * time.Millisecond)
	if n, m := rec.snapshot(); n != 0 || len(m) != 0 {
		t.Fatalf("restarts %d messages %v for a stopped chat", n, m)
	}
}

func TestBusyTimeoutStillTellsTheOwnerAndTheAgent(t *testing.T) {
	f, rec := keptWindowFixture()
	f.s.researchWait = 20 * time.Millisecond
	setBusy(f.c, "turn-1")
	if _, err := f.s.SetResearchMode(context.Background(), "s1", true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "failure message", func() bool { _, m := rec.snapshot(); return len(m) == 1 })
	if f.c.researchActive() || f.s.researchCarried.has("s1") || !strings.Contains(f.lines[len(f.lines)-1], "not enabled") {
		t.Fatalf("flag %v lines %v", f.c.researchActive(), f.lines)
	}
}
