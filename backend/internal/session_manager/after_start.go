package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

func (m *Manager) deliverAfterStartPrompt(ctx context.Context, agent ports.Agent, cfg ports.LaunchConfig, handle ports.RuntimeHandle, id domain.SessionID, prompt string) error {
	selector, selects := agent.(ports.AgentAfterStartCommand)
	command := ""
	if selects {
		var err error
		command, err = selector.AfterStartCommand(ctx, cfg)
		if err != nil {
			return err
		}
	}
	if prompt == "" && command == "" {
		return nil
	}
	if err := m.waitForPromptReadiness(ctx, agent, cfg, handle); err != nil {
		return err
	}
	if command != "" {
		if err := m.deliverStartedInput(ctx, id, command); err != nil {
			return err
		}
		if err := m.waitForAfterStartCommand(ctx, selector, cfg, handle); err != nil {
			return err
		}
	}
	if prompt == "" {
		return nil
	}
	return m.deliverStartedInput(ctx, id, prompt)
}

func (m *Manager) waitForAfterStartCommand(ctx context.Context, selector ports.AgentAfterStartCommand, cfg ports.LaunchConfig, handle ports.RuntimeHandle) error {
	rendered, ok := m.runtime.(ports.StyledTerminalOutputReader)
	if !ok {
		return errors.New("native startup command requires current rendered terminal output")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		output, err := rendered.GetStyledOutput(ctx, handle, 100)
		if err != nil {
			return fmt.Errorf("read native startup command output: %w", err)
		}
		if selector.AfterStartCommandApplied(cfg, output) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("native startup command was not confirmed")
		case <-ticker.C:
		}
	}
}

func (m *Manager) deliverStartedInput(ctx context.Context, id domain.SessionID, prompt string) error {
	// Call Deliver directly (not the Guard.Send wrapper, which folds a suppressed
	// outcome into nil): a freshly-spawned session can terminate or hit a
	// permission dialog between readiness and prompt injection, and folding that
	// into success would report a spawn/restore that never delivered its prompt.
	var outcome sessionguard.Outcome
	var err error
	if m.SessionMutationInProgress(id) {
		outcome, err = m.messenger.DeliverUnderMutation(ctx, id, prompt)
	} else {
		outcome, err = m.messenger.Deliver(ctx, id, prompt)
	}
	if err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	switch outcome {
	case sessionguard.SuppressedNotFound:
		return fmt.Errorf("send %s: %w", id, ErrNotFound)
	case sessionguard.SuppressedTerminated:
		return fmt.Errorf("send %s: %w", id, ErrTerminated)
	case sessionguard.SuppressedExited:
		return fmt.Errorf("send %s: %w", id, ErrAgentExited)
	case sessionguard.SuppressedAwaitingUser:
		return fmt.Errorf("send %s: %w", id, ErrAwaitingDecision)
	case sessionguard.SuppressedStartupPending:
		return fmt.Errorf("send %s: %w", id, ErrStartupPending)
	case sessionguard.SuppressedInputGated:
		return fmt.Errorf("send %s: %w", id, ErrOperationInProgress)
	case sessionguard.SuppressedUnknown:
		return fmt.Errorf("send %s: pre-write session read failed", id)
	default:
		return nil
	}
}

func buildAfterStartPrompt(ctx context.Context, agent ports.Agent, cfg ports.LaunchConfig) (string, error) {
	if builder, ok := agent.(ports.AgentAfterStartPromptBuilder); ok {
		return builder.BuildAfterStartPrompt(ctx, cfg)
	}
	return cfg.Prompt, nil
}

func (m *Manager) waitForPromptReadiness(ctx context.Context, agent ports.Agent, cfg ports.LaunchConfig, handle ports.RuntimeHandle) error {
	provider, ok := agent.(ports.AgentPromptReadinessProvider)
	if !ok {
		return nil
	}
	hints, err := provider.PromptReadinessHints(ctx, cfg)
	if err != nil {
		return fmt.Errorf("prompt readiness: %w", err)
	}
	callerDeadline, hasCallerDeadline := ctx.Deadline()
	if hints.InitialDelay > 0 {
		if hasCallerDeadline && time.Until(callerDeadline)-promptDeliveryDeadlineReserve <= hints.InitialDelay {
			m.logger.Warn("prompt readiness skipped to preserve caller deadline for fallback delivery",
				"sessionID", cfg.SessionID,
				"kind", string(cfg.Kind),
				"configuredTimeout", hints.Timeout.String(),
			)
			return nil
		}
		if err := sleepContext(ctx, hints.InitialDelay); err != nil {
			return err
		}
	}
	if len(hints.Patterns) == 0 || hints.Timeout <= 0 {
		return nil
	}
	poll := hints.PollInterval
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	lines := hints.Lines
	if lines <= 0 {
		lines = 80
	}

	waitTimeout, hasReadinessBudget := promptReadinessWaitTimeout(hints.Timeout, callerDeadline, hasCallerDeadline)
	if !hasReadinessBudget {
		m.logger.Warn("prompt readiness skipped to preserve caller deadline for fallback delivery",
			"sessionID", cfg.SessionID,
			"kind", string(cfg.Kind),
			"configuredTimeout", hints.Timeout.String(),
		)
		return nil
	}

	deadline := time.NewTimer(waitTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		output, err := m.runtime.GetOutput(ctx, handle, lines)
		if err == nil && promptOutputContains(output, hints.Patterns) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			// Prompt readiness is best-effort: a missing terminal marker must not
			// block spawn forever or be treated as confirmed readiness. Fall back
			// to delivering the prompt and make the degraded path observable.
			m.logger.Warn("prompt readiness timed out; falling back to after-start prompt delivery",
				"sessionID", cfg.SessionID,
				"kind", string(cfg.Kind),
				"timeout", waitTimeout.String(),
				"configuredTimeout", hints.Timeout.String(),
				"pollInterval", poll.String(),
				"lines", lines,
			)
			return nil
		case <-ticker.C:
		}
	}
}

func promptReadinessWaitTimeout(configured time.Duration, callerDeadline time.Time, hasCallerDeadline bool) (time.Duration, bool) {
	if !hasCallerDeadline {
		return configured, true
	}
	remaining := time.Until(callerDeadline) - promptDeliveryDeadlineReserve
	if remaining <= 0 {
		return 0, false
	}
	return min(configured, remaining), true
}

// promptOutputEscape matches CSI and OSC sequences. The runtime returns the raw
// output ring, so a styled prompt such as Kimi's "│ >" arrives with a colour
// code inside it and never matched until the readiness wait timed out.
var promptOutputEscape = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07]*(?:\x07|\x1b\\))`)

func promptOutputContains(output string, patterns []string) bool {
	plain := promptOutputEscape.ReplaceAllString(output, "")
	for _, pattern := range patterns {
		if pattern != "" && (strings.Contains(output, pattern) || strings.Contains(plain, pattern)) {
			return true
		}
	}
	return false
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
