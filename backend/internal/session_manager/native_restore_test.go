package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

type interactiveTestAgent struct {
	fakeAgent
	plan        *interactiveTestPlan
	preflight   error
	directCalls int
}

func (a *interactiveTestAgent) PrepareNativeRestore(context.Context, ports.RestoreConfig) (ports.NativeRestoreInitialization, error) {
	if a.preflight != nil {
		return nil, a.preflight
	}
	return a.plan, a.preflight
}
func (a *interactiveTestAgent) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	a.directCalls++
	return nil, false, errors.New("direct resume forbidden")
}

type interactiveTestPlan struct {
	ready    func(context.Context) (bool, error)
	restored func(context.Context) (bool, error)
}

func (*interactiveTestPlan) Argv() []string { return []string{"native"} }
func (*interactiveTestPlan) LaunchEnv() map[string]string {
	return map[string]string{"restore-test": "1"}
}
func (*interactiveTestPlan) ResumeInput() string { return "/resume agent-x" }
func (*interactiveTestPlan) TargetID() string    { return "agent-x" }
func (p *interactiveTestPlan) Ready(ctx context.Context, _ string, _ string) (bool, error) {
	if p.ready != nil {
		return p.ready(ctx)
	}
	return true, nil
}
func (p *interactiveTestPlan) Restored(ctx context.Context, _ string, _ string) (bool, error) {
	if p.restored != nil {
		return p.restored(ctx)
	}
	return true, nil
}

type nativeBindingLCM struct {
	*fakeLCM
	discard bool
}

func (l *nativeBindingLCM) ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error {
	if l.discard {
		return nil
	}
	if err := l.fakeLCM.ApplyActivitySignal(ctx, id, s); err != nil {
		return err
	}
	r := l.store.sessions[id]
	if s.AgentSessionID != "" {
		r.Metadata.AgentSessionID = s.AgentSessionID
		r.Metadata.AgentSessionIDLaunchID = s.LaunchID
	}
	l.store.sessions[id] = r
	return nil
}

type renderedRestoreRuntime struct {
	*fakeRuntime
	calls int
	err   error
}

func (r *renderedRestoreRuntime) GetStyledOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	r.calls++
	return "CURRENT RENDERED SCREEN", r.err
}

func interactiveManager(t *testing.T, terminated bool, a *interactiveTestAgent) (*Manager, *fakeStore, *fakeRuntime, *fakeMessenger) {
	t.Helper()
	rt := &fakeRuntime{}
	m, st, _ := newExitedResumeManager(t, rt, a)
	m.runtime = &renderedRestoreRuntime{fakeRuntime: rt}
	r := st.sessions["mer-1"]
	r.IsTerminated = terminated
	r.Harness = domain.AgentHarness("memcode")
	r.Metadata.AgentSessionIDLaunchID = "launch-old"
	st.sessions[r.ID] = r
	m.lcm = &nativeBindingLCM{fakeLCM: &fakeLCM{store: st}}
	msg := &fakeMessenger{}
	m.messenger = sessionguard.New(st, msg, m.logger)
	m.messenger.SetInputLease(m)
	return m, st, rt, msg
}

func TestInteractiveCrashReconcileRefusesUnverifiedBootstrap(t *testing.T) {
	t.Parallel()

	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, st, rt, _ := interactiveManager(t, false, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionIDLaunchID = "previous"
	rec.Metadata.RuntimeLaunchID = "launch-old"
	rec.Activity.State = domain.ActivityActive
	st.sessions[rec.ID] = rec
	rt.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	if err := m.reconcileLive(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].Activity.State == domain.ActivityExited {
		t.Fatal("healthy runtime destroyed for a missing start-hook identity")
	}
}
func TestInteractiveActualStartupHealthRefusesUnverifiedBootstrap(t *testing.T) {
	t.Parallel()

	a := &interactiveTestAgent{plan: &interactiveTestPlan{}}
	m, st, rt, _ := interactiveManager(t, false, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionIDLaunchID = "previous"
	rec.Activity.State = domain.ActivityActive
	st.sessions[rec.ID] = rec
	rt.aliveByHandle = map[string]bool{"tmux-mer-1": true}
	if err := m.checkSessionHealth(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rt.destroyed != 0 || st.sessions[rec.ID].Activity.State == domain.ActivityExited {
		t.Fatal("startup destroyed healthy runtime")
	}
}
