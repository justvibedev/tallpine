package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type healthCheckLauncher struct {
	recordingLauncher
	checks []ChatStart
	err    error
}

func (l *healthCheckLauncher) StartChat(_ context.Context, cfg ChatStart) (ChatStarted, error) {
	l.checks = append(l.checks, cfg)
	return ChatStarted{}, l.err
}

func TestStartupHealthNeverLaunchesStoppedSessions(t *testing.T) {
	t.Parallel()

	// Chat sessions whose host died are resumed instead; see
	// TestStartupResumesChatsWhoseHostDied.
	ctx := context.Background()
	m, st, rt, ws := newLifecycleManager()
	launcher := &healthCheckLauncher{err: ports.ErrChatHostNotRunning}
	m.chat = launcher
	st.sessions["mer-tui"] = domain.SessionRecord{
		ID: "mer-tui", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/tui", Branch: "tp/tui/root", RuntimeHandleID: "dead"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
	st.sessions["mer-saved"] = domain.SessionRecord{
		ID: "mer-saved", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, IsTerminated: true,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/saved", Branch: "tp/saved/root", AgentSessionID: "native"},
		Activity: domain.Activity{State: domain.ActivityExited},
	}
	st.worktrees["mer-saved"] = []domain.SessionWorktreeRecord{{SessionID: "mer-saved", RepoName: domain.RootWorkspaceRepoName, State: "removed"}}
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatalf("startup launched runtime or restored workspace: runtimes=%d restores=%d", rt.created, len(ws.restoreConfigs))
	}
	if len(launcher.checks) != 0 {
		t.Fatalf("startup started a Chat for TUI sessions: %+v", launcher.checks)
	}
	if st.sessions["mer-tui"].Activity.State != domain.ActivityExited || st.sessions["mer-tui"].IsTerminated {
		t.Fatalf("mer-tui must stay stopped and resumable: %+v", st.sessions["mer-tui"])
	}
	if !st.sessions["mer-saved"].IsTerminated || len(st.worktrees["mer-saved"]) != 1 {
		t.Fatal("startup changed a shutdown-saved session or its restore marker")
	}
}

func TestStartupHealthInconclusiveChatProbePreservesSession(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	launcher := &healthCheckLauncher{err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	rec := domain.SessionRecord{ID: "mer-chat", ProjectID: "mer", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "tp/chat/root", ProviderConversationID: "thread-1"}, Activity: domain.Activity{State: domain.ActivityActive}}
	st.sessions[rec.ID] = rec
	if err := m.checkSessionHealth(context.Background(), rec); !errors.Is(err, ports.ErrChatRecoveryInconclusive) {
		t.Fatalf("error=%v", err)
	}
	if st.sessions[rec.ID].Activity.State != domain.ActivityActive || rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatal("uncertain probe changed activity or launched resources")
	}
}

func TestStartupHealthUnavailableRuntimeProbePreservesSession(t *testing.T) {
	t.Parallel()

	m, st, rt, ws := newLifecycleManager()
	rt.aliveErr = ports.ErrRuntimeUnavailable
	rec := domain.SessionRecord{
		ID: "mer-tui", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/tui", Branch: "tp/tui/root", RuntimeHandleID: "tmux-1"},
		Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions[rec.ID] = rec

	err := m.checkSessionHealth(context.Background(), rec)
	if !errors.Is(err, ports.ErrRuntimeUnavailable) {
		t.Fatalf("error=%v, want runtime unavailable", err)
	}
	if st.sessions[rec.ID].Activity.State != domain.ActivityActive || rt.created != 0 || len(ws.restoreConfigs) != 0 {
		t.Fatal("unavailable runtime probe changed activity or launched resources")
	}
}

// flakyStartupLauncher fails reattach attempts until failures runs out, then
// reports a live controller the way a successful reconnect does.
type flakyStartupLauncher struct {
	recordingLauncher
	mu       sync.Mutex
	failures int
	err      error
	attempts []ChatStart
	attached bool
}

func (l *flakyStartupLauncher) StartChat(_ context.Context, cfg ChatStart) (ChatStarted, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts = append(l.attempts, cfg)
	if l.failures != 0 {
		l.failures--
		return ChatStarted{}, l.err
	}
	l.attached = true
	return ChatStarted{}, nil
}

func (l *flakyStartupLauncher) HasLiveChatController(domain.SessionID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.attached
}

func (l *flakyStartupLauncher) attemptCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.attempts)
}

func (l *flakyStartupLauncher) setAttached() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attached = true
}

func startupChatRecord() domain.SessionRecord {
	return domain.SessionRecord{
		ID: "mer-chat", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
		Metadata: domain.SessionMetadata{WorkspacePath: "/ws/chat", Branch: "tp/chat/root", ProviderConversationID: "thread-1"},
		Activity: domain.Activity{State: domain.ActivityIdle},
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStartupChatCheckRetriesTransientFailure(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		// The production failure: the check spent its deadline waiting for the writer.
		"deadline":     fmt.Errorf("reconcile conversation authentication: %w", context.DeadlineExceeded),
		"inconclusive": fmt.Errorf("persistent host: %w", ports.ErrChatRecoveryInconclusive),
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m, st, rt, ws := newLifecycleManager()
			launcher := &flakyStartupLauncher{failures: 2, err: cause}
			m.chat = launcher
			m.startupChatRetryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
			rec := startupChatRecord()
			st.sessions[rec.ID] = rec
			if err := m.ReconcileBackground(ctx); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "the retry to reattach", func() bool { return launcher.HasLiveChatController(rec.ID) })
			waitFor(t, "ready readiness", func() bool { return m.SessionStatusReadiness(st.sessions[rec.ID]) == "ready" })
			if got := launcher.attemptCount(); got != 3 {
				t.Fatalf("attempts = %d, want the startup check plus two retries", got)
			}
			for i, attempt := range launcher.attempts {
				if !attempt.ReconnectOnly {
					t.Fatalf("attempt %d may launch a provider: %+v", i, attempt)
				}
			}
			if rt.created != 0 || len(ws.restoreConfigs) != 0 || st.sessions[rec.ID].Activity != rec.Activity {
				t.Fatal("retry launched resources or changed activity")
			}
		})
	}
}

func TestStartupChatCheckDoesNotRetryPermanentFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: errors.New("provider rejected the permission mode")}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := launcher.attemptCount(); got != 1 {
		t.Fatalf("attempts = %d, want no retry", got)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "unavailable" {
		t.Fatalf("readiness = %s, want unavailable so Resume is offered", got)
	}
}

func TestStartupChatCheckRetryStopsOnceReattached(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{20 * time.Millisecond, time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	launcher.setAttached() // Resume reconnected before the first retry.
	time.Sleep(80 * time.Millisecond)
	if got := launcher.attemptCount(); got != 1 {
		t.Fatalf("attempts = %d, want the retry to leave a reattached chat alone", got)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "ready" {
		t.Fatalf("readiness = %s, want the reattached chat settled as ready", got)
	}
}

func TestStartupChatCheckStaysCheckingWhileRetryPending(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: 1, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{100 * time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	// The startup attempt failed, but a reattach is scheduled: clients must show
	// a reconnect in progress, not offer Resume.
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "checking" {
		t.Fatalf("readiness while a retry is pending = %s, want checking", got)
	}
	waitFor(t, "the retry to reattach", func() bool { return launcher.HasLiveChatController(rec.ID) })
	waitFor(t, "ready readiness", func() bool { return m.SessionStatusReadiness(st.sessions[rec.ID]) == "ready" })
}

func TestStartupChatCheckRetryGivesUpAfterSchedule(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := &flakyStartupLauncher{failures: -1, err: ports.ErrChatRecoveryInconclusive}
	m.chat = launcher
	m.startupChatRetryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	rec := startupChatRecord()
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "every scheduled retry", func() bool { return launcher.attemptCount() == 3 })
	time.Sleep(20 * time.Millisecond)
	if got := launcher.attemptCount(); got != 3 {
		t.Fatalf("attempts = %d, want one per scheduled delay", got)
	}
	if got := m.SessionStatusReadiness(st.sessions[rec.ID]); got != "unavailable" {
		t.Fatalf("readiness = %s, want unavailable so Resume is offered", got)
	}
}

// Builds before background branch naming left this state; it is still rolled back.
func TestStartupHealthCleansOnlyUncommittedSetup(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{"clean", "dirty", "committed", "runtime", "provider", "setup-done"} {
		t.Run(scenario, func(t *testing.T) {
			m, st, ws, _ := newGitTaskPreparationManager(t)
			rec, err := st.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Mode: domain.SessionModeTUI, Harness: domain.HarnessClaudeCode, ClientRequestID: "create-worker", ProvisionSteps: []domain.SessionProvisionStep{{ID: domain.SessionProvisionStepSetup, Status: domain.SessionProvisionStepRunning}}})
			if err != nil {
				t.Fatal(err)
			}
			root, err := ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "mer", SessionID: rec.ID, Branch: "tp/login/root", BaseBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			rec.Metadata.WorkspacePath, rec.Metadata.Branch, rec.Metadata.WorkspaceRepoPath = root.Path, root.Branch, root.RepoPath
			switch scenario {
			case "dirty":
				if err := os.WriteFile(filepath.Join(root.Path, "keep"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "committed":
				rec.ClientRequestCommitted = true
			case "runtime":
				rec.Metadata.RuntimeHandleID = "live-runtime"
			case "provider":
				rec.Metadata.ProviderConversationID = "live-provider"
			case "setup-done":
				rec.ProvisionSteps[0].Status = domain.SessionProvisionStepDone
			}
			st.sessions[rec.ID] = rec
			_ = m.checkSessionHealth(t.Context(), rec)
			_, found := st.sessions[rec.ID]
			_, pathErr := os.Stat(root.Path)
			if scenario == "clean" {
				if found || !errors.Is(pathErr, os.ErrNotExist) {
					t.Fatalf("unfinished clean request remains: found=%v path=%v", found, pathErr)
				}
			} else if !found || pathErr != nil {
				t.Fatalf("protected request or workspace lost: found=%v path=%v", found, pathErr)
			}
		})
	}
}

// deadHostLauncher models Chats whose provider host died with the previous
// daemon: a reconnect-only check finds no host, and a full start (Resume)
// launches a controller after failures more failed attempts.
type deadHostLauncher struct {
	recordingLauncher
	mu       sync.Mutex
	failures int
	live     map[domain.SessionID]bool
	checks   map[domain.SessionID]int
	resumes  map[domain.SessionID]int
	drains   map[domain.SessionID]int
}

func newDeadHostLauncher() *deadHostLauncher {
	return &deadHostLauncher{
		live: map[domain.SessionID]bool{}, checks: map[domain.SessionID]int{}, resumes: map[domain.SessionID]int{},
		drains: map[domain.SessionID]int{},
	}
}

func (l *deadHostLauncher) DrainChatQueue(_ context.Context, id domain.SessionID) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.drains[id]++
	return nil
}

func (l *deadHostLauncher) StartChat(_ context.Context, cfg ChatStart) (ChatStarted, error) {
	l.mu.Lock()
	if cfg.ReconnectOnly {
		l.checks[cfg.SessionID]++
		l.mu.Unlock()
		return ChatStarted{}, ports.ErrChatHostNotRunning
	}
	l.resumes[cfg.SessionID]++
	if l.failures > 0 {
		l.failures--
		l.mu.Unlock()
		return ChatStarted{}, errors.New("session/load timed out")
	}
	defer l.mu.Unlock()
	l.live[cfg.SessionID] = true
	return ChatStarted{}, nil
}

func (l *deadHostLauncher) HasLiveChatController(id domain.SessionID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.live[id]
}

func (l *deadHostLauncher) snapshot(id domain.SessionID) (checks, resumes int, live bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.checks[id], l.resumes[id], l.live[id]
}

func deadHostChat(id domain.SessionID) domain.SessionRecord {
	rec := startupChatRecord()
	rec.ID = id
	rec.Metadata.WorkspacePath = "/ws/" + string(id)
	rec.Metadata.Branch = "tp/" + string(id) + "/root"
	return rec
}

func TestStartupResumesChatsWhoseHostDied(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, rt, _ := newLifecycleManager()
	launcher := newDeadHostLauncher()
	m.chat = launcher
	m.startupChatResumeSem = make(chan struct{}, 1) // the fake store is not safe for concurrent writers
	active := []domain.SessionID{"mer-orch", "mer-w1"}
	for _, id := range active {
		st.sessions[id] = deadHostChat(id)
	}
	killed := deadHostChat("mer-killed")
	killed.IsTerminated = true
	killed.Activity.State = domain.ActivityExited
	st.sessions[killed.ID] = killed
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range active {
		waitFor(t, string(id)+" to resume", func() bool { _, _, live := launcher.snapshot(id); return live })
		if checks, resumes, _ := launcher.snapshot(id); checks != 1 || resumes != 1 {
			t.Fatalf("%s: checks=%d resumes=%d, want one reconnect check then one resume", id, checks, resumes)
		}
		// Messages queued behind the lost turn go out once the agent is back.
		waitFor(t, string(id)+" to drain its queue", func() bool {
			launcher.mu.Lock()
			defer launcher.mu.Unlock()
			return launcher.drains[id] == 1
		})
	}
	for _, id := range active {
		if reason, _ := st.SessionAgentExitReason(ctx, id); reason != domain.AgentExitUnknown {
			t.Fatalf("%s: exit reason %q survived the resume", id, reason)
		}
	}
	time.Sleep(20 * time.Millisecond)
	if checks, resumes, _ := launcher.snapshot(killed.ID); checks != 0 || resumes != 0 || !st.sessions[killed.ID].IsTerminated {
		t.Fatalf("user-terminated session was touched: checks=%d resumes=%d", checks, resumes)
	}
	if rt.created != 0 {
		t.Fatalf("Chat resume launched %d TUI runtimes", rt.created)
	}
}

func TestStartupChatResumeRetriesFailedResume(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := newDeadHostLauncher()
	launcher.failures = 2
	m.chat = launcher
	m.startupChatResumeDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	rec := deadHostChat("mer-chat")
	st.sessions[rec.ID] = rec
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the retried resume", func() bool { _, _, live := launcher.snapshot(rec.ID); return live })
	if _, resumes, _ := launcher.snapshot(rec.ID); resumes != 3 {
		t.Fatalf("resumes = %d, want two failures and one success", resumes)
	}
}

func TestStartupChatResumeBoundsConcurrency(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, _, _, _ := newLifecycleManager()
	release := make(chan struct{})
	var mu sync.Mutex
	inFlight, maxFlight, resumed := 0, 0, 0
	m.startupChatResume = func(context.Context, domain.SessionID) (bool, error) {
		mu.Lock()
		inFlight++
		maxFlight = max(maxFlight, inFlight)
		mu.Unlock()
		<-release
		mu.Lock()
		defer mu.Unlock()
		inFlight--
		resumed++
		return true, nil
	}
	m.resumeStartupChats(ctx, []domain.SessionID{"mer-a", "mer-b", "mer-c", "mer-d", "mer-e"})
	waitFor(t, "the first resumes", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return inFlight == defaultStartupChatResumeWorkers
	})
	time.Sleep(20 * time.Millisecond) // a broken bound would admit more by now
	close(release)
	waitFor(t, "every resume", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return resumed == 5
	})
	mu.Lock()
	defer mu.Unlock()
	if maxFlight != defaultStartupChatResumeWorkers {
		t.Fatalf("max concurrent resumes = %d, want %d", maxFlight, defaultStartupChatResumeWorkers)
	}
}

// deadHostSendLauncher refuses relays while no controller is live, as the Chat
// service does for a session whose host died, and records resume queueing.
type deadHostSendLauncher struct {
	*deadHostLauncher
	queued []string
}

func (l *deadHostSendLauncher) RelaySessionChatTurn(_ context.Context, id domain.SessionID, _, _ string, _ ports.MessageDeliveryOptions) (string, error) {
	if l.HasLiveChatController(id) {
		return "turn-relay", nil
	}
	return "", errors.New("no live chat controller for session")
}

func (l *deadHostSendLauncher) QueueRelayForResume(_ context.Context, _ domain.SessionID, text, _ string, _ ports.MessageDeliveryOptions) (ports.MessageReceipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queued = append(l.queued, text)
	return ports.MessageReceipt{Outcome: ports.MessageQueued, ID: "queued-1"}, nil
}

// A send to a Chat whose host died must be accepted and delivered by a resume,
// not fail with no controller (finding C).
func TestSendToChatWhoseHostDiedQueuesAndResumes(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	launcher := &deadHostSendLauncher{deadHostLauncher: newDeadHostLauncher()}
	m.chat = launcher
	rec := deadHostChat("mer-chat")
	rec.Activity.State = domain.ActivityExited
	st.sessions[rec.ID] = rec
	_ = st.SetSessionAgentExitReason(context.Background(), rec.ID, domain.AgentExitHostLost) // the startup check found its host gone

	receipt, err := m.SendWithReceipt(context.Background(), rec.ID, "hello", nil, ports.MessageDeliveryOptions{})
	if err != nil {
		t.Fatalf("send to a stopped Chat failed: %v", err)
	}
	if receipt.Outcome != ports.MessageQueued {
		t.Fatalf("receipt = %+v, want queued", receipt)
	}
	waitFor(t, "the resume to deliver the queue", func() bool {
		launcher.mu.Lock()
		defer launcher.mu.Unlock()
		return launcher.live[rec.ID] && launcher.drains[rec.ID] == 1
	})
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	if len(launcher.queued) != 1 || launcher.resumes[rec.ID] != 1 {
		t.Fatalf("queued=%v resumes=%d, want the message queued once and one resume", launcher.queued, launcher.resumes[rec.ID])
	}
}

// Only an agent that was live when the daemon went down is resumed. One the
// user stopped with Exit Agent, or whose exit reason is unknown (older rows, a
// task that finished), stays stopped across restarts.
func TestStartupDoesNotResumeStoppedChats(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, st, _, _ := newLifecycleManager()
	launcher := newDeadHostLauncher()
	m.chat = launcher
	m.startupChatResumeSem = make(chan struct{}, 1)
	userStopped := deadHostChat("mer-user")
	userStopped.Activity.State = domain.ActivityExited
	st.sessions[userStopped.ID] = userStopped
	_ = st.SetSessionAgentExitReason(ctx, userStopped.ID, domain.AgentExitUser)
	unknown := deadHostChat("mer-unknown")
	unknown.Activity.State = domain.ActivityExited
	st.sessions[unknown.ID] = unknown
	live := deadHostChat("mer-live")
	st.sessions[live.ID] = live
	if err := m.ReconcileBackground(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the live Chat to resume", func() bool { _, _, ok := launcher.snapshot(live.ID); return ok })
	time.Sleep(20 * time.Millisecond)
	for _, id := range []domain.SessionID{userStopped.ID, unknown.ID} {
		if _, resumes, isLive := launcher.snapshot(id); resumes != 0 || isLive {
			t.Fatalf("%s was resumed at boot: resumes=%d live=%v", id, resumes, isLive)
		}
	}
	if reason, _ := st.SessionAgentExitReason(ctx, userStopped.ID); reason != domain.AgentExitUser {
		t.Fatalf("user stop reason = %q, want it kept", reason)
	}
}

// A send to a Chat the user stopped neither queues nor relaunches it; the
// sender is told the agent was stopped instead of getting an internal error.
func TestSendToUserStoppedChatIsRefusedWithoutResume(t *testing.T) {
	t.Parallel()

	for name, reason := range map[string]domain.AgentExitReason{"user": domain.AgentExitUser, "unknown": domain.AgentExitUnknown} {
		t.Run(name, func(t *testing.T) {
			m, st, _, _ := newLifecycleManager()
			launcher := &deadHostSendLauncher{deadHostLauncher: newDeadHostLauncher()}
			m.chat = launcher
			rec := deadHostChat("mer-chat")
			rec.Activity.State = domain.ActivityExited
			st.sessions[rec.ID] = rec
			_ = st.SetSessionAgentExitReason(context.Background(), rec.ID, reason)

			_, err := m.SendWithReceipt(context.Background(), rec.ID, "hello", nil, ports.MessageDeliveryOptions{})
			if !errors.Is(err, ErrAgentStoppedByUser) {
				t.Fatalf("send to a stopped Chat = %v, want ErrAgentStoppedByUser", err)
			}
			time.Sleep(20 * time.Millisecond)
			launcher.mu.Lock()
			defer launcher.mu.Unlock()
			if len(launcher.queued) != 0 || launcher.resumes[rec.ID] != 0 {
				t.Fatalf("queued=%v resumes=%d, want neither", launcher.queued, launcher.resumes[rec.ID])
			}
		})
	}
}

// The refusal is harness-agnostic: a stopped DeepSeek Harness Chat answers
// AGENT_STOPPED, and is neither queued nor resumed, like claude-code and codex.
func TestSendToStoppedDeepSeekChatIsRefusedWithoutResume(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newLifecycleManager()
	launcher := &deadHostSendLauncher{deadHostLauncher: newDeadHostLauncher()}
	m.chat = launcher
	rec := deadHostChat("mer-dsh")
	rec.Harness = domain.HarnessDeepSeek
	rec.Activity.State = domain.ActivityExited
	st.sessions[rec.ID] = rec
	_ = st.SetSessionAgentExitReason(context.Background(), rec.ID, domain.AgentExitUser)

	_, err := m.SendWithReceipt(context.Background(), rec.ID, "hello", nil, ports.MessageDeliveryOptions{})
	if !errors.Is(err, ErrAgentStoppedByUser) {
		t.Fatalf("send to a stopped deepseek-harness Chat = %v, want ErrAgentStoppedByUser", err)
	}
	launcher.mu.Lock()
	defer launcher.mu.Unlock()
	if len(launcher.queued) != 0 || launcher.resumes[rec.ID] != 0 {
		t.Fatalf("queued=%v resumes=%d, want neither", launcher.queued, launcher.resumes[rec.ID])
	}
}
