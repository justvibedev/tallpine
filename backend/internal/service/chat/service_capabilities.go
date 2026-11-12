package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// SupportsChat reports whether a harness has a Chat driver at all, without
// probing the local install. Use it to decide whether Chat is even offerable.
func (s *Service) SupportsChat(harness domain.AgentHarness) bool {
	return s.drivers.SupportsChat(harness)
}

// PreflightChat reports whether a harness can start in chat mode right now.
//
// Called before any durable state exists, so an unsupported request costs nothing
// — no terminated orphan row, no wasted worktree. It never downgrades to TUI:
// that would put the user in a terminal they did not ask for.
func (s *Service) PreflightChat(
	ctx context.Context,
	harness domain.AgentHarness,
	permissions ports.PermissionMode,
	decision agentpolicy.Decision,
) error {
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return fmt.Errorf("%w: %s has no chat driver", ports.ErrChatUnsupported, harness)
	}
	if err := admitChatFence(driver, harness, decision, permissions); err != nil {
		return err
	}
	caps, err := s.driverCapabilities(ctx, harness, driver)
	if err != nil {
		return err
	}
	return capabilityAdmissionError(harness, caps, permissions, decision)
}

// PermissionModes lists, strictest first, the permission modes harness can run a
// chat in: the ones its capabilities admit (Pi cannot ask, so only bypass) and
// its agent accepts (Kimi runs only in its default). It starts nothing.
func (s *Service) PermissionModes(ctx context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error) {
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return nil, fmt.Errorf("%w: %s has no chat driver", ports.ErrChatUnsupported, harness)
	}
	caps, err := s.driverCapabilities(ctx, harness, driver)
	if err != nil {
		return nil, err
	}
	checker, _ := driver.(ports.ChatPermissionModeChecker)
	var modes []ports.PermissionMode
	for _, mode := range ports.PermissionModesStrictestFirst {
		if len(ports.MissingCapabilitiesForPermissions(caps, mode)) == 0 && (checker == nil || checker.SupportsPermissionMode(mode)) {
			modes = append(modes, mode)
		}
	}
	return modes, nil
}

func capabilityAdmissionError(
	harness domain.AgentHarness,
	caps ports.ChatCapabilities,
	permissions ports.PermissionMode,
	decision agentpolicy.Decision,
) error {
	missing := ports.MissingCapabilitiesForPermissions(caps, permissions)
	if len(missing) == 0 {
		return nil
	}
	var allowed []ports.PermissionMode
	if !permissioncap.Of(decision).ForbidsBypass() &&
		ports.NormalizePermissionMode(permissions) != ports.PermissionModeBypassPermissions &&
		len(ports.MissingCapabilitiesForPermissions(caps, ports.PermissionModeBypassPermissions)) == 0 {
		allowed = []ports.PermissionMode{ports.PermissionModeBypassPermissions}
	}
	return &ports.ChatCapabilityError{
		Harness:                harness,
		Missing:                append([]ports.ChatCapability(nil), missing...),
		AllowedPermissionModes: allowed,
	}
}

// ChatUnavailable maps each of harnesses whose chat adapter is missing to the
// reason. It starts nothing, so it is cheap enough for a picker to ask.
func (s *Service) ChatUnavailable(ctx context.Context, harnesses []domain.AgentHarness) map[domain.AgentHarness]string {
	out := map[domain.AgentHarness]string{}
	for _, harness := range harnesses {
		driver, err := s.drivers.Driver(harness)
		if err != nil {
			continue
		}
		if checker, ok := driver.(ports.ChatInstallChecker); ok {
			if err := checker.CheckChatInstall(ctx); err != nil {
				out[harness] = err.Error()
			}
		}
	}
	return out
}

// branchProviderRebinder moves a branch that never had a turn to a new
// provider conversation.
type branchProviderRebinder interface {
	RebindUnansweredBranchProvider(ctx context.Context, conversationID, branchID, previous, provider string) error
}

// rebindBranchOnReady wraps ready so a committed fresh start also rebinds the
// branch that named the conversation the provider never saved.
func (s *Service) rebindBranchOnReady(
	ctx context.Context,
	ready func(StartResult) (ControllerCommit, error),
	conversationID, branchID, previous string,
) func(StartResult) (ControllerCommit, error) {
	return func(started StartResult) (ControllerCommit, error) {
		commit := ControllerCommit{Conversation: started.Conversation}
		if ready != nil {
			var err error
			if commit, err = ready(started); err != nil {
				return commit, err
			}
		}
		if rebinder, ok := s.store.(branchProviderRebinder); ok {
			if err := rebinder.RebindUnansweredBranchProvider(context.WithoutCancel(ctx), conversationID, branchID, previous, started.ProviderConversationID); err != nil {
				s.log.Warn("rebind unanswered switch branch", "branch", branchID, "error", err)
			}
		}
		return commit, nil
	}
}

// withHandoffNote adds a switch's handoff note to the agent's system prompt. A
// branch keeps its note for life, so every launch on it passes the note again.
func withHandoffNote(systemPrompt, note string) string {
	if note == "" {
		return systemPrompt
	}
	return strings.TrimSpace(strings.TrimSpace(systemPrompt) + "\n\nTallpine provider handoff context:\n\n" + note)
}

// switchBranchUnanswered reports a branch a switch opened whose agent never
// finished a turn, so its provider may never have saved the conversation.
// Resuming it would fail forever; it can start fresh, since its handoff note
// holds the context. A turn the agent did not complete (a daemon restart that
// cut off the first turn after a Claude /clear, B673) is not an answer. The
// fresh start applies only when the provider itself reports the id missing,
// so a provider that did save a partial turn still resumes it.
func (s *Service) switchBranchUnanswered(ctx context.Context, conversationID string, branch domain.ConversationBranch) bool {
	if s.reader == nil || branch.Strategy != domain.ConversationBranchStrategyApproximateContext || branch.HandoffNote == "" {
		return false
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		return false
	}
	for _, turn := range rows.Turns {
		if turn.BranchID == branch.ID && turn.State.Answered() {
			return false
		}
	}
	return true
}

// PrewarmChat runs PreflightChat's capability probe in the background so a
// later switch to harness finds it cached: the probe's throwaway provider
// process (up to ~1.3s) otherwise lands on the switch. Failures are not
// cached, so a later switch still reports them. One check per harness runs at a time.
func (s *Service) PrewarmChat(harness domain.AgentHarness) {
	driver, err := s.drivers.Driver(harness)
	if err != nil {
		return
	}
	if _, running := s.prewarming.LoadOrStore(harness, struct{}{}); running {
		return
	}
	go func() {
		defer s.prewarming.Delete(harness)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = s.driverCapabilities(ctx, harness, driver)
	}()
}

// driverCapabilities performs the provider capability probe once per harness for
// the lifetime of this service. Reconciliation can resume many sessions using
// the same provider; launching a throwaway provider process for every one makes
// startup scale with twice the number of sessions. Only successful probes are
// cached, so a repaired install can be retried without a daemon restart. The raw
// capabilities are cached because admission depends on each session's requested
// permission mode.
func (s *Service) driverCapabilities(
	ctx context.Context,
	harness domain.AgentHarness,
	driver ports.ChatDriver,
) (ports.ChatCapabilities, error) {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if caps, ok := s.probed[harness]; ok {
		return caps, nil
	}
	caps, err := driver.Probe(ctx)
	if err != nil {
		return nil, err
	}
	s.probed[harness] = caps
	return caps, nil
}

// StartChat launches the controller for a freshly created session.
func (s *Service) StartChat(ctx context.Context, cfg StartConfig) (StartResult, error) {
	controller, err := s.Start(ctx, cfg)
	if err != nil {
		return StartResult{}, err
	}
	return controllerStartResult(controller, nil, nil), nil
}

// StartResult is the durable outcome of a launch.
type StartResult = ports.ChatControllerStarted

// StartChatTurn delivers the initial prompt as a normal turn.
//
// It goes through the controller rather than the mode-gated Send, because the
// session row is still being written when spawn calls this: reading the persisted
// mode here would race the write that sets it.
func (s *Service) StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	controller, err := s.Controller(id)
	if err != nil {
		return "", err
	}
	turn, err := controller.Send(ctx, ports.ChatUserMessage{
		Text: text,
		// The initial prompt is the user's task brief. Origin records who AUTHORED
		// a message, not who delivered it — the daemon carrying it to the provider
		// no more makes it the daemon's message than the network makes it the
		// network's. Attributing it to the daemon rendered the user's own request
		// as a system notice.
		Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		return "", err
	}
	return turn.ID, nil
}
