package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// gatedResumeLauncher holds every provider start until the test releases it
// with an outcome, then fails if the start's own context ended meanwhile: the
// signal a resume cut off by its caller's deadline gives in production.
type gatedResumeLauncher struct {
	*deadHostLauncher
	entered chan struct{}
	release chan error
	starts  atomic.Int32
}

func newGatedResumeLauncher() *gatedResumeLauncher {
	return &gatedResumeLauncher{
		deadHostLauncher: newDeadHostLauncher(),
		entered:          make(chan struct{}, 8),
		release:          make(chan error, 8),
	}
}

func (l *gatedResumeLauncher) StartChat(ctx context.Context, cfg ChatStart) (ChatStarted, error) {
	if !cfg.ReconnectOnly {
		l.starts.Add(1)
		l.entered <- struct{}{}
		if err := <-l.release; err != nil {
			return ChatStarted{}, err
		}
		if err := ctx.Err(); err != nil {
			return ChatStarted{}, err
		}
	}
	return l.deadHostLauncher.StartChat(ctx, cfg)
}

func newGatedResumeManager(t *testing.T) (*Manager, *gatedResumeLauncher, domain.SessionID) {
	t.Helper()
	m, st, _, _ := newLifecycleManager()
	launcher := newGatedResumeLauncher()
	m.chat = launcher
	rec := deadHostChat("mer-chat")
	rec.Activity.State = domain.ActivityExited
	st.sessions[rec.ID] = rec
	if err := st.SetSessionAgentExitReason(context.Background(), rec.ID, domain.AgentExitHostLost); err != nil {
		t.Fatal(err)
	}
	return m, launcher, rec.ID
}

func waitEntered(t *testing.T, l *gatedResumeLauncher) {
	t.Helper()
	select {
	case <-l.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("provider start never began")
	}
}

// A Resume pressed while a slow automatic resume holds the session joins it and
// returns its result, however long it takes, instead of a 409 after the join
// wait (agent-orchestrator-808 after the restart). Not parallel: it shortens
// the package's join wait.
func TestResumeAgent_JoinsSlowAutomaticResume(t *testing.T) {
	saved := resumeJoinWait
	resumeJoinWait = 20 * time.Millisecond
	t.Cleanup(func() { resumeJoinWait = saved })

	m, launcher, id := newGatedResumeManager(t)
	autoDone := make(chan bool, 1)
	go func() {
		owned, err := m.autoResumeJoined(context.Background(), id, false)
		if err != nil {
			t.Errorf("automatic resume: %v", err)
		}
		autoDone <- owned
	}()
	waitEntered(t, launcher)
	userDone := make(chan error, 1)
	go func() {
		_, err := m.ResumeAgentWithMode(context.Background(), id)
		userDone <- err
	}()
	time.Sleep(10 * resumeJoinWait) // far past the old join wait
	launcher.release <- nil
	if err := <-userDone; err != nil {
		t.Fatalf("Resume during an automatic resume = %v, want its result", err)
	}
	if !<-autoDone {
		t.Fatal("automatic resume did not see the user join it")
	}
	if n := launcher.starts.Load(); n != 1 {
		t.Fatalf("provider starts = %d, want the one shared attempt", n)
	}
}

// A manual Resume outlives the request that started it: the request answers
// ErrResumeStillRunning at its deadline, the provider start and its history
// load keep their own budget, and the next press joins the same attempt.
func TestResumeAgent_OutlivesShortRequest(t *testing.T) {
	t.Parallel()

	m, launcher, id := newGatedResumeManager(t)
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.ResumeAgentWithMode(short, id); !errors.Is(err, ErrResumeStillRunning) {
		t.Fatalf("Resume past its request deadline = %v, want ErrResumeStillRunning", err)
	}
	waitEntered(t, launcher)
	second := make(chan error, 1)
	go func() {
		_, err := m.ResumeAgentWithMode(context.Background(), id)
		second <- err
	}()
	launcher.release <- nil
	if err := <-second; err != nil {
		t.Fatalf("second Resume = %v, want the first attempt's success", err)
	}
	if _, _, live := launcher.snapshot(id); !live {
		t.Fatal("resume died with its request")
	}
	if n := launcher.starts.Load(); n != 1 {
		t.Fatalf("provider starts = %d, want one", n)
	}
}

// When the owner's Resume shares a failing startup attempt, the startup loop
// stops retrying (so it cannot hold the lock against the next press), the
// owner gets the attempt's real error rather than "already being resumed", and
// pressing Resume again starts a fresh attempt that works.
func TestStartupChatResumeLeavesRetryToJoinedUser(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, launcher, id := newGatedResumeManager(t)
	m.startupChatResumeDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	m.resumeStartupChats(ctx, []domain.SessionID{id})
	waitEntered(t, launcher)
	userDone := make(chan error, 1)
	go func() {
		_, err := m.ResumeAgentWithMode(context.Background(), id)
		userDone <- err
	}()
	waitFor(t, "the Resume to join the startup attempt", func() bool {
		m.agentOpMu.Lock()
		defer m.agentOpMu.Unlock()
		flight := m.resumeFlights[id]
		return flight != nil && flight.auto && flight.userJoined && flight.joiners == 1
	})
	launcher.release <- errors.New("ACP initialize: Request cancelled")
	err := <-userDone
	if err == nil || errors.Is(err, ErrResumeInProgress) || !strings.Contains(err.Error(), "Request cancelled") {
		t.Fatalf("joined Resume error = %v, want the attempt's own failure", err)
	}
	waitFor(t, "the startup loop to end", func() bool {
		m.startupChatResumeMu.Lock()
		defer m.startupChatResumeMu.Unlock()
		_, pending := m.startupChatResumePending[id]
		return !pending
	})
	if n := launcher.starts.Load(); n != 1 {
		t.Fatalf("provider starts = %d, want no automatic retry after the user joined", n)
	}
	launcher.release <- nil
	if _, err := m.ResumeAgentWithMode(context.Background(), id); err != nil {
		t.Fatalf("Resume after the startup loop ended = %v", err)
	}
	if _, _, live := launcher.snapshot(id); !live {
		t.Fatal("Resume after the startup loop ended did not start the agent")
	}
}

// A second Resume while one is running joins it: same result, no second
// runtime restart (blockingRestartRuntime would panic on one), never a 409.
func TestResumeAgent_JoinsConcurrentRequest(t *testing.T) {
	t.Parallel()

	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"tmux-mer-1": true}}
	runtime := &blockingRestartRuntime{
		fakeRuntime: baseRuntime,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex", "resume", "agent-x"}}}
	m, _, _ := newExitedResumeManager(t, runtime, agent)
	firstDone := make(chan error, 1)
	go func() {
		_, err := m.ResumeAgentWithMode(ctx, "mer-1")
		firstDone <- err
	}()
	<-runtime.entered

	secondDone := make(chan error, 1)
	go func() {
		_, err := m.ResumeAgentWithMode(ctx, "mer-1")
		secondDone <- err
	}()
	waitFor(t, "the second Resume to join", func() bool {
		m.agentOpMu.Lock()
		defer m.agentOpMu.Unlock()
		flight := m.resumeFlights["mer-1"]
		return flight != nil && flight.joiners == 1
	})
	close(runtime.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first resume: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("concurrent resume error = %v, want the shared attempt's success", err)
	}
}
