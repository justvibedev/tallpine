package chat

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func loggedCompactionService(def *string, buf *bytes.Buffer) *Service {
	s := compactionService(def)
	s.log = slog.New(slog.NewTextHandler(buf, nil))
	return s
}

// B398: an orchestrator reattached after a daemon restart (its launch window
// unknown) with a worker-sized Settings default of 40% on a 1M model must be
// moved to the orchestrator's 120k, not to 400k, and the applied window is
// logged for the session.
func TestReattachedOrchestratorMovesToTheOrchestratorDefault(t *testing.T) {
	def := "40%"
	var buf bytes.Buffer
	s := loggedCompactionService(&def, &buf)
	s.projects = compactProjects{"p": {}}
	conv := &liveWindowConv{applied: true}
	c := claudeController(conv, compactLiveUnknown, "")
	c.kind = domain.KindOrchestrator
	c.conversation.ProjectID = "p"

	if s.reconcileCompaction(context.Background(), c) {
		t.Fatal("reconcile reported pending after the bridge applied the window")
	}
	if len(conv.calls) != 1 || conv.calls[0] != 120_000 {
		t.Fatalf("live window calls = %v, want [120000]", conv.calls)
	}
	if got := buf.String(); !strings.Contains(got, "compaction window applied live") || !strings.Contains(got, "session=s1") || !strings.Contains(got, "tokens=120000") {
		t.Fatalf("missing applied-window log line:\n%s", got)
	}
}

// A bridge that keeps its launch window is logged, so a window that never
// reached the agent is visible in the daemon log.
func TestReconcileLogsAWindowThatWaitsForARestart(t *testing.T) {
	def := ""
	var buf bytes.Buffer
	s := loggedCompactionService(&def, &buf)
	s.projects = compactProjects{"p": {}}
	c := claudeController(&liveWindowConv{applied: false}, 400_000, "")
	c.kind = domain.KindOrchestrator
	c.conversation.ProjectID = "p"
	if !s.reconcileCompaction(context.Background(), c) {
		t.Fatal("reconcile reported applied for a bridge that kept its window")
	}
	if got := buf.String(); !strings.Contains(got, "waits for a restart") || !strings.Contains(got, "desired=120000") || !strings.Contains(got, "live=400000") {
		t.Fatalf("missing pending-window log line:\n%s", got)
	}
}

// A project's orchestrator.autoCompactWindow change reaches that project's
// running orchestrators without their own choice, and nothing else.
func TestApplyProjectCompactionReachesOnlyThatProjectsOrchestrators(t *testing.T) {
	def := ""
	s := compactionService(&def)
	projects := compactProjects{"p": {}, "q": {}}
	s.projects = projects
	mk := func(id domain.SessionID, kind domain.SessionKind, project domain.ProjectID, own string) *liveWindowConv {
		conv := &liveWindowConv{applied: true}
		c := claudeController(conv, 120_000, own)
		c.sessionID, c.kind, c.conversation.ProjectID = id, kind, project
		if s.controllers == nil {
			s.controllers = map[domain.SessionID]*Controller{}
		}
		s.controllers[id] = c
		return conv
	}
	target := mk("orch-p", domain.KindOrchestrator, "p", "")
	own := mk("orch-p-own", domain.KindOrchestrator, "p", "1m")
	other := mk("orch-q", domain.KindOrchestrator, "q", "")
	worker := mk("worker-p", domain.KindWorker, "p", "")

	projects["p"] = domain.ProjectConfig{Orchestrator: domain.RoleOverride{AutoCompactWindow: "200k"}}
	s.ApplyProjectCompaction(context.Background(), "p")

	if len(target.calls) != 1 || target.calls[0] != 200_000 {
		t.Fatalf("project orchestrator calls = %v, want [200000]", target.calls)
	}
	for name, conv := range map[string]*liveWindowConv{"own choice": own, "other project": other, "worker": worker} {
		if len(conv.calls) != 0 {
			t.Errorf("%s: calls = %v, want none", name, conv.calls)
		}
	}
}

// A project's env window change reaches a live orchestrator too: the launch
// baseline is refreshed from the project's current env, not kept (B398 review).
func TestApplyProjectCompactionFollowsAProjectEnvWindowChange(t *testing.T) {
	def := ""
	s := compactionService(&def)
	envCfg := func(v string) domain.ProjectConfig {
		return domain.ProjectConfig{Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: v}}
	}
	projects := compactProjects{"p": envCfg("300000")}
	s.projects = projects
	conv := &liveWindowConv{applied: true}
	c := claudeController(conv, 300_000, "")
	c.kind, c.conversation.ProjectID, c.compactBaseline = domain.KindOrchestrator, "p", 300_000
	s.controllers = map[domain.SessionID]*Controller{"s1": c}

	projects["p"] = envCfg("150000")
	s.ApplyProjectCompaction(context.Background(), "p")
	if len(conv.calls) != 1 || conv.calls[0] != 150_000 {
		t.Fatalf("env change calls = %v, want [150000]", conv.calls)
	}
	// Removing the env window leaves the orchestrator default.
	projects["p"] = domain.ProjectConfig{}
	s.ApplyProjectCompaction(context.Background(), "p")
	if len(conv.calls) != 2 || conv.calls[1] != 120_000 {
		t.Fatalf("env removed calls = %v, want [... 120000]", conv.calls)
	}
}

// A project config write rewrites the baseline while snapshots and turns read
// it; run with -race (B398 review).
func TestApplyProjectCompactionRacesNoBaselineReader(t *testing.T) {
	def := ""
	s := compactionService(&def)
	s.projects = compactProjects{"p": {Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "150000"}}}
	c := claudeController(&liveWindowConv{applied: true}, 150_000, "")
	c.kind, c.conversation.ProjectID = domain.KindOrchestrator, "p"
	s.controllers = map[domain.SessionID]*Controller{"s1": c}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 50 {
			s.ApplyProjectCompaction(context.Background(), "p")
		}
	}()
	for range 50 {
		_ = s.desiredCompactTokens(context.Background(), c)
	}
	<-done
}
