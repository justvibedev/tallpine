package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

// Send delivers a message to a running session's agent through the guarded
// pane-write primitive, then best-effort confirms the agent actually accepted
// it. The guard refuses delivery into a session that is gone, terminated, has
// an exited agent, or is paused on a permission decision;
// those refusals surface as typed sentinels so the API reports why instead of
// silently dropping the message. Tallpine has no delivery ack: the messenger returns
// nil the moment the runtime paste + Enter commands exit 0, and for a large
// multiline prompt a single Enter may not submit (claude-code leaves it as an
// unsubmitted draft). confirmActive observes the durable Activity.State
// (flipped to active by the user-prompt-submit hook) and re-sends Enter until
// the session is active or the budget is exhausted. When budget is exhausted
// without confirmation, it surfaces ErrDeliveryUnconfirmed.
func (m *Manager) Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error {
	// Chat keeps its established automation relay attribution unless a trusted
	// caller supplies the explicit user-authored fact through SendWithOptions.
	return m.SendWithOptions(ctx, id, message, attachment, ports.MessageDeliveryOptions{})
}

// SendWithOptions delivers a message with caller-supplied authorship facts that
// are independent of the mechanism Tallpine uses to carry it.
func (m *Manager) SendWithOptions(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error {
	_, err := m.SendWithReceipt(ctx, id, message, attachment, options)
	return err
}

// SendWithReceipt is SendWithOptions that also reports whether the message was
// delivered to the agent or queued for later, with a correlation id when the
// delivery path has one.
func (m *Manager) SendWithReceipt(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error) {
	if attachment != nil {
		// Reuses StageAttachments rather than a bespoke writer: it already owns the
		// empty-workspace guard (refusing beats writing under the daemon's cwd),
		// randomized naming safe for a session sent to repeatedly, directory
		// creation, and the git-exclude step.
		refs, err := m.StageAttachments(ctx, id, []ports.SpawnAttachment{*attachment})
		if err != nil {
			return ports.MessageReceipt{}, fmt.Errorf("send %s: attachment: %w", id, err)
		}
		message = appendAttachmentReferences(message, refs)
	}
	options.InteractionAt = m.clock()
	var receipt ports.MessageReceipt
	err := m.deliver(ctx, id, message, "", options, &receipt)
	return receipt, err
}

// SendSemantic delivers an internal message and returns only after the target
// agent has accepted that exact message. Chat uses its native accepted-turn
// boundary. Supported TUIs correlate the durable id through UserPromptSubmit;
// a successful pane write alone never satisfies this method.
func (m *Manager) SendSemantic(ctx context.Context, id domain.SessionID, message, clientMessageID string) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		handled, sendErr := m.sendChat(ctx, id, message, clientMessageID, ports.MessageDeliveryOptions{})
		if !handled {
			return ErrSemanticAcceptanceUnsupported
		}
		return sendErr
	}
	if !m.harnessSemanticAcceptance(rec.Harness) {
		return ErrSemanticAcceptanceUnsupported
	}
	if semanticMessageAccepted(rec, clientMessageID) {
		m.semanticPastes.forget(id, clientMessageID)
		return nil
	}
	launch := rec.Metadata.RuntimeLaunchID
	if m.semanticPastes.written(id, clientMessageID, launch) {
		if semanticTurnUnfinished(rec) {
			// Already typed into this turn's input queue; another paste would only
			// repeat the batch to the agent. Return at once: waiting cannot succeed
			// and would hold the single report delivery loop.
			return fmt.Errorf("%w: waiting for the running turn", ErrSemanticAcceptancePending)
		}
		// A turn just ended, and the agent may be about to submit the copy already
		// typed. Give it the acceptance window before typing a second one.
		current, accepted, err := m.awaitSemanticAcceptance(ctx, id, clientMessageID)
		if err != nil || accepted {
			return err
		}
		if semanticTurnUnfinished(current) {
			return fmt.Errorf("%w: waiting for the running turn", ErrSemanticAcceptancePending)
		}
		launch = current.Metadata.RuntimeLaunchID
	}
	wrapped := domain.WrapReportDelivery(clientMessageID, message)
	if err := m.send(ctx, id, wrapped, clientMessageID, ports.MessageDeliveryOptions{}); err != nil {
		// A pane write whose activity confirmation could not be observed is not
		// fatal here: SendSemantic's own durable acceptance loop below is
		// authoritative and correlates this exact message through its submission
		// marker. Any other send error is a real failure.
		if !errors.Is(err, ErrDeliveryUnconfirmed) {
			return err
		}
	}
	m.semanticPastes.record(id, clientMessageID, launch)
	current, accepted, err := m.awaitSemanticAcceptance(ctx, id, clientMessageID)
	if err != nil || accepted {
		return err
	}
	if semanticTurnUnfinished(current) {
		return fmt.Errorf("%w: prompt submission was not observed during the running turn", ErrSemanticAcceptancePending)
	}
	return fmt.Errorf("%w: prompt submission was not observed", ErrSemanticAcceptanceUnsupported)
}

func (m *Manager) harnessSemanticAcceptance(harness domain.AgentHarness) bool {
	if m.agents == nil {
		return false
	}
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return false
	}
	signaler, ok := agent.(ports.SemanticMessageAcceptanceSignaler)
	return ok && signaler.EmitsSemanticMessageAcceptance()
}

func semanticMessageAccepted(rec domain.SessionRecord, clientMessageID string) bool {
	return clientMessageID != "" &&
		rec.Metadata.ConversationCheckpointState == domain.ConversationCheckpointCoordination &&
		rec.Metadata.ConversationCheckpointGeneration == rec.Metadata.RuntimeLaunchID &&
		rec.Metadata.ConversationCheckpointTurnID == clientMessageID
}

// InterruptTUI requests cancellation through the session's owned runtime.
func (m *Manager) InterruptTUI(ctx context.Context, id domain.SessionID) error {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat || !m.harnessSemanticAcceptance(rec.Harness) {
		return ErrSemanticAcceptanceUnsupported
	}
	interrupter, ok := m.runtime.(runtimeInterrupter)
	if !ok || rec.Metadata.RuntimeHandleID == "" {
		return ErrSemanticAcceptanceUnsupported
	}
	return interrupter.Interrupt(ctx, ports.RuntimeHandle{ID: rec.Metadata.RuntimeHandleID})
}

// send carries an optional idempotency key used by durable transition-message
// retries. Ordinary callers leave it empty; the outbox preserves the key across
// restart, rollback, and even a second overlapping handoff.
func (m *Manager) send(ctx context.Context, id domain.SessionID, message, clientMessageID string, options ports.MessageDeliveryOptions) error {
	return m.deliver(ctx, id, message, clientMessageID, options, nil)
}

// deliver is send that fills receipt (when non-nil) with the delivery outcome.
func (m *Manager) deliver(ctx context.Context, id domain.SessionID, message, clientMessageID string, options ports.MessageDeliveryOptions, receipt *ports.MessageReceipt) error {
	// A session stored under a removed harness is read-only. Refuse before
	// queueing or writing anything, with the explanation the API surfaces.
	if rec, ok, err := m.store.GetSession(ctx, id); err == nil && ok && rec.Harness.IsUnsupported() {
		return fmt.Errorf("send %s: %w", id, harnessLookupError(rec.Harness))
	}
	// A controller transition deliberately has a short interval with no writer.
	// Queue internal/lifecycle sends durably instead of racing either controller
	// or dropping coordination work; the transition worker drains this outbox
	// only after the target controller is active.
	if queuedID, err := m.queueDuringInterfaceTransition(ctx, id, message, clientMessageID, options); err != nil {
		return fmt.Errorf("send %s: interface transition: %w", id, err)
	} else if queuedID != "" {
		if receipt != nil {
			*receipt = ports.MessageReceipt{Outcome: ports.MessageQueued, ID: queuedID}
		}
		return nil
	}
	// Chat mode has no pane to type into, so it does not go through the messenger
	// at all. Without this branch the send reached the runtime guard and was
	// refused as "missing runtime handles" — true of the handles, wrong about the
	// session, and it left `tallpine send` and orchestrator-to-worker relay unable to
	// reach a chat worker.
	if handled, err := m.sendChatReceipt(ctx, id, message, clientMessageID, options, receipt); handled {
		return err
	}

	message, deliveryHandle, rawSlash := m.prepareDeliveryMessage(ctx, id, message, clientMessageID, options)
	if options.InteractionAt.IsZero() {
		options.InteractionAt = m.clock()
	}
	afterWrite, evidence := m.deliveryAcceptance(id, message, options)
	// A non-empty send must not land in a composer that holds an unsent human
	// draft (#5711), whether it is a tallpine send or a worker report: a refused report
	// is deferred and retried by report.Coordinator (ErrOrchestratorBusy), so
	// draft protection does not lose it. Only an empty message is left unchecked —
	// it is the deliberate Enter that submits an existing draft. The check runs
	// under the guard's held input lease, immediately before the write.
	var composerBusy func(context.Context, domain.SessionRecord) (bool, func())
	if strings.TrimSpace(message) != "" {
		composerBusy = m.composerBusyCheck()
	}
	// Keystrokes accepted after this instant were typed while or after this send
	// wrote; confirmActive must not press Enter on them. Wall time, like the
	// terminal gate's lastInputAt.
	inputBaseline := time.Now()
	outcome, err := m.messenger.DeliverWithComposerCheck(ctx, id, message, composerBusy, afterWrite)
	if err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	if err := suppressedDeliveryError(id, outcome); err != nil {
		return err
	}
	if receipt != nil {
		*receipt = ports.MessageReceipt{Outcome: ports.MessageDelivered, ID: deliveryHandle}
	}
	// Chat and transition queues persist interaction in their acceptance transaction.
	// Only direct terminal sends need this separate fact; outbox replay already has it.
	if options.SenderSessionID != "" && clientMessageID == "" {
		if recorder, ok := m.store.(ports.SessionInteractionRecorder); ok {
			if err := recorder.RecordSessionInteraction(context.WithoutCancel(ctx), id, options.SenderSessionID, options.InteractionAt); err != nil {
				return fmt.Errorf("record interaction: %w", err)
			}
		}
	}
	// confirmActive only helps — and is only SAFE — when the harness reports
	// both a prompt-submit signal (so the loop can observe active) and a
	// blocked signal it can clear mid-turn (so it can tell an unsubmitted
	// draft from a pending permission dialog and never Enter into the latter).
	// Only claude-code and its hook-delegators (grok/continueagent/devin)
	// satisfy both; every other harness opts out via EmitsBlockedActivity —
	// see ports.BlockedActivitySignaler.
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		// This lookup only decides whether the harness is confirm-eligible; a
		// failure here cannot tell, so confirmation is skipped and the already
		// delivered message is left as sent. Confirmation itself (below) can
		// still fail the send with ErrDeliveryUnconfirmed once it runs. Log so a
		// store error is not swallowed silently.
		m.logger.Warn("send: confirm skipped, session lookup failed", "sessionID", id, "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	if !rawSlash && m.harnessNudgeSafe(rec.Harness) {
		if err := m.confirmActive(ctx, m.messenger, id, evidence, inputBaseline); err != nil {
			return fmt.Errorf("send %s: %w", id, err)
		}
	}
	return nil
}

// harnessNudgeSafe reports whether the session's harness is safe to nudge with
// an Enter-only re-send (see ports.SubmitActivitySignaler and
// ports.BlockedActivitySignaler): it must emit BOTH a prompt-submit signal
// (else the loop wastes its budget never observing active) and a blocked
// signal (else an Enter meant to resubmit a draft could answer a permission
// dialog the harness cannot report).
func (m *Manager) harnessNudgeSafe(harness domain.AgentHarness) bool {
	if m.agents == nil {
		return false
	}
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return false
	}
	sub, ok := agent.(ports.SubmitActivitySignaler)
	if !ok || !sub.EmitsSubmitActivity() {
		return false
	}
	blk, ok := agent.(ports.BlockedActivitySignaler)
	return ok && blk.EmitsBlockedActivity()
}

func (m *Manager) harnessStartupSignalGatesInput(harness domain.AgentHarness) bool {
	if m.agents == nil {
		return false
	}
	agent, ok := m.agents.Agent(harness)
	if !ok {
		return false
	}
	signaler, ok := agent.(ports.StartupInputReadinessSignaler)
	return ok && signaler.FirstSignalProvesInputReady()
}

// Built-in informational commands delivered raw (unwrapped, no confirmation).
// Only commands that print inline and return to the composer qualify: a
// command that opens a modal panel would leave the TUI inside a dialog that no
// hook reports, and custom commands/skills start a turn, so both stay wrapped.
// /usage is a modal settings tab in claude-code, so it is raw elsewhere only.
var (
	rawSlashCommands      = map[string]bool{"/context": true, "/cost": true}
	rawSlashNonClaudeOnly = map[string]bool{"/usage": true}
	// rawSlashKiroOnly are commands verified on kiro-cli 3.0.0 only: /compact
	// opens the command picker, one Enter runs it inline and the composer
	// returns. Wrapped in a delivery envelope it would reach the model as text.
	rawSlashKiroOnly = map[string]bool{"/compact": true}
)

// isSlashCommand reports whether message is a single-line raw-eligible
// built-in command (e.g. "/context"), with any arguments, for the harness.
func isSlashCommand(message string, harness domain.AgentHarness) bool {
	m := strings.TrimSpace(message)
	if !strings.HasPrefix(m, "/") || strings.ContainsAny(m, "\n\r") {
		return false
	}
	token := strings.Fields(m)[0]
	return rawSlashCommands[token] || (rawSlashNonClaudeOnly[token] && harness != domain.HarnessClaudeCode) ||
		(rawSlashKiroOnly[token] && (harness == domain.HarnessKiro || harness == ""))
}

// unprefixOrchestratorSlash drops the CLI's "[from <sender>] " prefix from an
// allowlisted informational command sent by an orchestrator to a worker of its
// own project, so the harness sees "/usage" rather than chat text. Any other
// sender, project or command keeps the prefix and is wrapped as before.
func (m *Manager) unprefixOrchestratorSlash(ctx context.Context, id domain.SessionID, message string, options ports.MessageDeliveryOptions) string {
	sender := options.SenderSessionID
	if sender == "" || options.AuthoredByUser {
		return message
	}
	body, found := strings.CutPrefix(message, "[from "+sender+"] ")
	if !found {
		return message
	}
	target, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !isSlashCommand(body, target.Harness) {
		return message
	}
	from, ok, err := m.store.GetSession(ctx, domain.SessionID(sender))
	if err != nil || !ok || from.Kind != domain.KindOrchestrator || from.ProjectID == "" || from.ProjectID != target.ProjectID {
		return message
	}
	return body
}

// prepareDeliveryMessage frames a message for the terminal. A raw slash command
// (a non-user send that would otherwise be wrapped) is acted on by the harness
// itself and starts no turn, so there is no prompt-submit to wait for and no
// Enter to nudge. User-authored sends are never wrapped and keep their
// confirmation.
func (m *Manager) prepareDeliveryMessage(ctx context.Context, id domain.SessionID, message, clientMessageID string, options ports.MessageDeliveryOptions) (string, string, bool) {
	deliveryHandle := clientMessageID
	_, coordination := domain.CoordinationDeliveryID(message)
	message = m.unprefixOrchestratorSlash(ctx, id, message, options)
	rawSlash := false
	// The harness-less check is a superset of every harness-aware one, so it
	// only spares the store read for messages that cannot be a raw slash.
	if !coordination && !options.AuthoredByUser && isSlashCommand(message, "") {
		if rec, ok, err := m.store.GetSession(ctx, id); err == nil && ok {
			rawSlash = isSlashCommand(message, rec.Harness)
		}
	}
	if !coordination && !options.AuthoredByUser && strings.TrimSpace(message) != "" {
		deliveryID := clientMessageID
		if deliveryID == "" {
			deliveryID = "session-send:" + m.newLaunchID()
		}
		deliveryHandle = deliveryID
		if rawSlash {
			// The harness only treats input starting with "/" as a command.
			message = strings.TrimSpace(message)
		} else {
			message = domain.WrapSessionDelivery(deliveryID, message)
		}
	}
	return message, deliveryHandle, rawSlash
}

// deliveryAcceptance builds the post-write hook and the evidence the
// confirmation loop honors when Active is never observed.
func (m *Manager) deliveryAcceptance(id domain.SessionID, message string, options ports.MessageDeliveryOptions) (func(context.Context) error, deliveryEvidence) {
	var afterWrite func(context.Context) error
	coordinationID, internalReportDelivery := domain.CoordinationDeliveryID(message)
	if strings.TrimSpace(message) != "" && !internalReportDelivery && options.AuthoredByUser {
		if recorder, ok := m.store.(latestUserPromptRecorder); ok {
			afterWrite = func(writeCtx context.Context) error {
				if _, recordErr := recorder.RecordSessionLatestUserPrompt(writeCtx, id, boundedConversationFact(message), options.InteractionAt); recordErr != nil {
					m.logger.Warn("send: delivered message but failed to persist latest user prompt", "sessionID", id, "error", recordErr)
				}
				return nil
			}
		}
	}
	// Evidence the confirmation loop honors when Active is never observed (the
	// prompt was accepted and the turn finished between activity polls). The two
	// send kinds leave different durable markers:
	//
	//   - A user-authored send records a prompt: afterWrite durably writes
	//     LatestUserPromptAt at options.InteractionAt as this delivery's own fact
	//     (kept because the provider hook may be lost). That exact instant is the
	//     baseline, so a LATER advance — the UserPromptSubmit hook landing after
	//     delivery — proves acceptance, while an unsubmitted draft leaves the
	//     marker at the baseline and still nudges. Fixing the baseline to the
	//     delivery instant (not a post-delivery store read) closes the race where
	//     the hook lands before that read and hides itself inside the baseline.
	//
	//   - A coordination (tallpine send) delivery deliberately leaves LatestUserPromptAt
	//     unchanged, so that marker is the wrong evidence for it. Its durable
	//     acceptance is the coordination checkpoint: ConversationCheckpointTurnID
	//     equal to this exact delivery ID, scoped to the current runtime launch
	//     (see semanticMessageAccepted). Matching the delivery ID avoids crediting
	//     an unrelated concurrent prompt.
	evidence := deliveryEvidence{}
	if afterWrite != nil {
		evidence.baselinePromptAt = options.InteractionAt
		evidence.recordsPrompt = true
	} else if internalReportDelivery {
		evidence.coordinationID = coordinationID
	}
	return afterWrite, evidence
}

// suppressedDeliveryError maps a guard suppression to its typed send error.
func suppressedDeliveryError(id domain.SessionID, outcome sessionguard.Outcome) error {
	switch outcome {
	case sessionguard.SuppressedComposerBusy:
		return fmt.Errorf("send %s: %w", id, ErrComposerBusy)
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
	}
	return nil
}
