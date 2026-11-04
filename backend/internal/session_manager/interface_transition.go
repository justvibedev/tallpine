package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	interfaceTransitionPoll               = 150 * time.Millisecond
	interfaceTransitionIdleSettle         = 750 * time.Millisecond
	interfaceTransitionSurfaceIdleSamples = 3
	interfaceTransitionStaleIdleLimit     = 5 * time.Second
	interfaceTransitionOutputLines        = 40
	interfaceInterruptSettle              = 2 * time.Second
	interfaceTransitionStopLimit          = 15 * time.Second
	interfaceTransitionStepLimit          = 45 * time.Second
	interfaceDeliveryRetry                = 2 * time.Second
	interfaceDeliveryIdlePoll             = 30 * time.Second
)

var (
	errDrainQuiescenceUnverified = errors.New("Tallpine could not verify that the terminal was idle after the latest input. The source interface was left untouched; retry after the terminal settles")                //nolint:staticcheck // ST1005: error text starts with the product name
	errDrainDecisionPending      = errors.New("Tallpine found a provider decision waiting in Terminal. The source interface was left untouched; answer it in Terminal and retry, or choose Cancel request and switch") //nolint:staticcheck // ST1005: error text starts with the product name
)

// interfaceTransitionStore is optional so the existing narrow Manager Store
// port and its many focused fakes do not grow methods unrelated to their tests.
// Production's SQLite store implements the full capability.
type interfaceTransitionStore interface {
	CreateSessionInterfaceTransition(context.Context, domain.SessionInterfaceTransition) (domain.SessionInterfaceTransition, bool, error)
	// CreateSessionInterfaceTransitionAfter atomically verifies the exact latest
	// terminal predecessor and claims the next transition. predecessorMatched is
	// false when another saga superseded the user's recovery consent.
	CreateSessionInterfaceTransitionAfter(context.Context, domain.SessionInterfaceTransition, string) (domain.SessionInterfaceTransition, bool, bool, error)
	GetSessionInterfaceTransition(context.Context, string) (domain.SessionInterfaceTransition, bool, error)
	GetActiveSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
	GetLatestSessionInterfaceTransition(context.Context, domain.SessionID) (domain.SessionInterfaceTransition, bool, error)
	ListActiveSessionInterfaceTransitions(context.Context) ([]domain.SessionInterfaceTransition, error)
	ListDeliverableSessionInterfaceTransitions(context.Context) ([]domain.SessionInterfaceTransition, error)
	AdvanceSessionInterfaceTransition(context.Context, string, domain.SessionInterfaceTransitionPhase, domain.SessionInterfaceTransitionPhase, string, string, string, time.Time) (bool, error)
	AcknowledgeSessionInterfaceTransitionNotice(context.Context, domain.SessionID, string, time.Time) (domain.SessionInterfaceTransition, bool, error)
	EnqueueSessionInterfaceTransitionMessage(context.Context, string, string, string, time.Time, ports.MessageDeliveryOptions) error
	ListPendingSessionInterfaceTransitionMessages(context.Context, string) ([]domain.SessionInterfaceTransitionMessage, error)
	MarkSessionInterfaceTransitionMessageDelivered(context.Context, int64, time.Time) error
}

type chatHandoffLauncher interface {
	ArmChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	PrepareChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	AbortChatHandoff(domain.SessionID)
}

type chatHandoffHistoryStore interface {
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	ConversationBranch(context.Context, string, string) (domain.ConversationBranch, error)
	HasConversationTurns(context.Context, string) (bool, error)
}

type runtimeInterrupter interface {
	Interrupt(context.Context, ports.RuntimeHandle) error
}

type interfaceTransitionRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// InterfaceTransitionStatus is the controller-facing view used by desktop and
// mobile to decide whether to draw the switch and to render any durable attempt.
type InterfaceTransitionStatus struct {
	Supported  bool
	TargetMode domain.SessionMode
	ReasonCode string
	Reason     string
	Transition *domain.SessionInterfaceTransition
}

// InterfaceTransitionStatus reports target availability plus the latest durable
// attempt. It checks native-conversation readiness and preflights a Chat target
// without launching a Chat session, so a missing driver runtime does not leave
// the switch enabled. Launch-time auth and races can still fail after status.
func (m *Manager) InterfaceTransitionStatus(
	ctx context.Context,
	id domain.SessionID,
) (InterfaceTransitionStatus, error) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil {
		return InterfaceTransitionStatus{}, fmt.Errorf("interface transition status %s: %w", id, err)
	}
	if !ok {
		return InterfaceTransitionStatus{}, ErrNotFound
	}
	target := oppositeSessionMode(rec.Mode)
	status := InterfaceTransitionStatus{TargetMode: target}
	if rec.IsTerminated {
		status.ReasonCode = "SESSION_TERMINATED"
		status.Reason = "Terminated sessions must be restored before switching interfaces."
	} else if interfaceWorkspaceUnavailable(rec) {
		status.ReasonCode = "WORKSPACE_UNAVAILABLE"
		status.Reason = "This session's workspace was never set up. Retry start before switching interfaces."
	} else if target == domain.SessionModeChat && (m.chat == nil || !m.chat.SupportsChat(rec.Harness)) {
		status.ReasonCode = "CHAT_UNSUPPORTED"
		status.Reason = fmt.Sprintf("%s does not support Chat UI.", rec.Harness)
	} else if target == domain.SessionModeChat {
		if err := m.preflightInterfaceTarget(ctx, rec, domain.SessionInterfaceTransition{TargetMode: target}); err != nil {
			status.ReasonCode = interfaceTransitionErrorCode(err)
			status.Reason = err.Error()
		}
	}
	if status.ReasonCode == "" {
		if _, err := m.handoffNativeConversationID(ctx, rec); err != nil {
			if errors.Is(err, ErrInterfaceHandoffUnsupported) {
				status.ReasonCode = "INTERFACE_HANDOFF_UNSUPPORTED"
			} else if errors.Is(err, ErrNativeConversationMissing) {
				status.ReasonCode = "NATIVE_SESSION_MISSING"
			} else {
				// Config-load or transcript-inspection blips must not hard-error
				// the polled status: report unverified so the client keeps
				// polling with the control disabled. Start still fails hard.
				status.ReasonCode = "NATIVE_SESSION_UNVERIFIED"
			}
			status.Reason = err.Error()
		} else {
			status.Supported = true
		}
	}
	if store, ok := m.store.(interfaceTransitionStore); ok {
		latest, found, err := store.GetLatestSessionInterfaceTransition(ctx, id)
		if err != nil {
			return InterfaceTransitionStatus{}, err
		}
		if found {
			status.Transition = &latest
		}
	}
	return status, nil
}

// interfaceWorkspaceUnavailable reports a session whose start failed before
// its workspace existed. Neither controller can launch without one, so a switch
// would only fail and leave both stopped. A session still provisioning is not
// refused: its workspace is on the way.
func interfaceWorkspaceUnavailable(rec domain.SessionRecord) bool {
	return strings.TrimSpace(rec.Metadata.WorkspacePath) == "" && !rec.ProvisionState.IsProvisioning()
}

// StartInterfaceTransition durably claims the session and starts the handoff in
// the background. The caller gets the transition immediately; long-running
// drain mode therefore does not depend on an HTTP request timeout.
func (m *Manager) StartInterfaceTransition(
	ctx context.Context,
	id domain.SessionID,
	target domain.SessionMode,
	policy domain.SessionInterfaceTransitionPolicy,
	historyPolicy domain.SessionInterfaceTransitionHistoryPolicy,
) (domain.SessionInterfaceTransition, error) {
	if !target.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("target mode %q is invalid", target)
	}
	if !policy.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("transition policy %q is invalid", policy)
	}
	if !historyPolicy.Valid() {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("transition history policy %q is invalid", historyPolicy)
	}
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return domain.SessionInterfaceTransition{}, ErrInterfaceHandoffUnsupported
	}
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !found {
		return domain.SessionInterfaceTransition{}, ErrNotFound
	}
	if rec.IsTerminated {
		return domain.SessionInterfaceTransition{}, ErrTerminated
	}
	source := domain.NormalizeSessionMode(rec.Mode)
	if target == source {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: session %s is already in %s mode",
			ErrInterfaceAlreadySelected, id, source)
	}
	if interfaceWorkspaceUnavailable(rec) {
		return domain.SessionInterfaceTransition{}, fmt.Errorf("%w: session %s", ErrInterfaceWorkspaceUnavailable, id)
	}
	nativeID, err := m.handoffNativeConversationID(ctx, rec)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	expectedPredecessorID := ""
	if historyPolicy == domain.SessionInterfaceTransitionHistoryProvider {
		latest, found, latestErr := store.GetLatestSessionInterfaceTransition(ctx, id)
		if latestErr != nil {
			return domain.SessionInterfaceTransition{}, latestErr
		}
		if source != domain.SessionModeTUI || target != domain.SessionModeChat ||
			!providerHistoryRecoveryAuthorized(id, nativeID, latest, found) {
			return domain.SessionInterfaceTransition{}, ErrInterfaceProviderHistoryRecoveryUnavailable
		}
		expectedPredecessorID = latest.ID
	}
	now := m.clock()
	pending := domain.SessionInterfaceTransition{
		ID: m.newLaunchID(), SessionID: id, SourceMode: source, TargetMode: target,
		Policy: policy, HistoryPolicy: historyPolicy, Phase: domain.SessionInterfaceTransitionRequested,
		NativeConversationID: nativeID, CreatedAt: now, UpdatedAt: now,
	}
	var transition domain.SessionInterfaceTransition
	var created bool
	if expectedPredecessorID != "" {
		var predecessorMatched bool
		transition, created, predecessorMatched, err = store.CreateSessionInterfaceTransitionAfter(
			ctx, pending, expectedPredecessorID,
		)
		if err == nil && !predecessorMatched {
			return domain.SessionInterfaceTransition{}, ErrInterfaceProviderHistoryRecoveryUnavailable
		}
	} else {
		transition, created, err = store.CreateSessionInterfaceTransition(ctx, pending)
	}
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !created {
		return transition, ErrInterfaceTransitionInProgress
	}
	if err := m.launchInterfaceTransition(ctx, transition); err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	return transition, nil
}

// launchInterfaceTransition fences a Chat source and starts the handoff worker
// for a freshly claimed transition.
func (m *Manager) launchInterfaceTransition(ctx context.Context, transition domain.SessionInterfaceTransition) error {
	if transition.SourceMode == domain.SessionModeChat {
		handoff, ok := m.chat.(chatHandoffLauncher)
		if !ok {
			err := ErrInterfaceHandoffUnsupported
			_ = m.finishInterfaceTransition(transition.ID, domain.SessionInterfaceTransitionFailed,
				"SOURCE_FENCE_FAILED", err.Error())
			return err
		}
		// This is the acceptance boundary visible to the caller. The transition
		// must not return while a Chat completion can still promote queued work;
		// target preflight and provider cancellation happen asynchronously later.
		if err := handoff.ArmChatHandoff(ctx, transition.SessionID, transition.Policy); err != nil {
			finishErr := m.finishInterfaceTransition(transition.ID,
				domain.SessionInterfaceTransitionFailed, "SOURCE_FENCE_FAILED", err.Error())
			if finishErr != nil {
				return errors.Join(err, fmt.Errorf("record source fence failure: %w", finishErr))
			}
			return err
		}
	}

	runCtx, cancel := context.WithCancel(context.Background())
	run := &interfaceTransitionRun{cancel: cancel, done: make(chan struct{})}
	m.transitionMu.Lock()
	m.transitions[transition.SessionID] = run
	m.transitionMu.Unlock()
	go m.runInterfaceTransition(runCtx, transition, run)
	return nil
}

func providerHistoryRecoveryAuthorized(
	sessionID domain.SessionID,
	nativeConversationID string,
	latest domain.SessionInterfaceTransition,
	found bool,
) bool {
	nativeConversationID = strings.TrimSpace(nativeConversationID)
	if !found || latest.SessionID != sessionID ||
		latest.SourceMode != domain.SessionModeTUI || latest.TargetMode != domain.SessionModeChat ||
		nativeConversationID == "" ||
		strings.TrimSpace(latest.NativeConversationID) != nativeConversationID {
		return false
	}
	if latest.Phase == domain.SessionInterfaceTransitionFailed &&
		latest.ErrorCode == "TARGET_HISTORY_UNTRUSTED_TEXT_MISMATCH" {
		return true
	}
	// Boot recovery deliberately restores the source controller instead of
	// continuing target startup. Retain only the explicit provider-history
	// consent carried by that exact interrupted saga so the user can retry it;
	// a strict or unrelated DAEMON_RESTARTED row must never manufacture consent.
	return latest.Phase == domain.SessionInterfaceTransitionRecovery &&
		latest.ErrorCode == "DAEMON_RESTARTED" &&
		latest.HistoryPolicy == domain.SessionInterfaceTransitionHistoryProvider
}

// CancelInterfaceTransition cancels only while the source controller is still
// intact. Once source_stopping begins, finishing or rolling back is safer than
// reopening a controller whose process state is ambiguous.
func (m *Manager) CancelInterfaceTransition(ctx context.Context, id domain.SessionID) error {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return ErrInterfaceHandoffUnsupported
	}
	transition, found, err := store.GetActiveSessionInterfaceTransition(ctx, id)
	if err != nil {
		return err
	}
	if !found {
		return ErrInterfaceTransitionNotFound
	}
	switch transition.Phase {
	case domain.SessionInterfaceTransitionRequested,
		domain.SessionInterfaceTransitionPreflighting,
		domain.SessionInterfaceTransitionDraining:
	default:
		return ErrInterfaceTransitionNotCancellable
	}
	// Cancellation must win durably before it signals the in-memory worker. If
	// the worker crossed into source_stopping after our read, this CAS loses and
	// we refuse the cancellation instead of acknowledging it while proceeding to
	// tear down the source controller.
	moved, err := store.AdvanceSessionInterfaceTransition(
		ctx,
		transition.ID,
		transition.Phase,
		domain.SessionInterfaceTransitionCancelled,
		transition.NativeConversationID,
		"TRANSITION_CANCELLED",
		transitionDetail(transition, "TRANSITION_CANCELLED", "The interface switch was cancelled."),
		m.clock(),
	)
	if err != nil {
		return err
	}
	if !moved {
		return ErrInterfaceTransitionNotCancellable
	}
	m.transitionMu.Lock()
	run := m.transitions[id]
	m.transitionMu.Unlock()
	if run != nil {
		run.cancel()
	}
	m.wakeTransitionMessageDispatcher()
	return nil
}

// AcknowledgeInterfaceTransitionNotice records that a failed/recovered handoff
// notice has been seen without deleting the transition's audit history. Both
// ids are checked so a stale client cannot accidentally dismiss a newer row or
// a transition owned by another session.
func (m *Manager) AcknowledgeInterfaceTransitionNotice(
	ctx context.Context,
	id domain.SessionID,
	transitionID string,
) (domain.SessionInterfaceTransition, error) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return domain.SessionInterfaceTransition{}, ErrInterfaceHandoffUnsupported
	}
	transition, found, err := store.GetSessionInterfaceTransition(ctx, transitionID)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !found || transition.SessionID != id {
		return domain.SessionInterfaceTransition{}, ErrInterfaceTransitionNotFound
	}
	if transition.Phase != domain.SessionInterfaceTransitionFailed &&
		transition.Phase != domain.SessionInterfaceTransitionRecovery {
		return domain.SessionInterfaceTransition{}, ErrInterfaceTransitionNoticeNotAcknowledgeable
	}
	acknowledged, changed, err := store.AcknowledgeSessionInterfaceTransitionNotice(
		ctx, id, transitionID, m.clock(),
	)
	if err != nil {
		return domain.SessionInterfaceTransition{}, err
	}
	if !changed {
		return domain.SessionInterfaceTransition{}, ErrInterfaceTransitionNoticeNotAcknowledgeable
	}
	return acknowledged, nil
}

func (m *Manager) hasActiveInterfaceTransition(ctx context.Context, id domain.SessionID) (bool, error) {
	store, ok := m.store.(interfaceTransitionStore)
	if !ok {
		return false, nil
	}
	_, active, err := store.GetActiveSessionInterfaceTransition(ctx, id)
	return active, err
}

func oppositeSessionMode(mode domain.SessionMode) domain.SessionMode {
	if domain.NormalizeSessionMode(mode) == domain.SessionModeChat {
		return domain.SessionModeTUI
	}
	return domain.SessionModeChat
}

func interfaceTransitionErrorCode(err error) string {
	switch {
	case errors.Is(err, ports.ErrChatUnsupported), errors.Is(err, ErrInterfaceHandoffUnsupported):
		return "INTERFACE_HANDOFF_UNSUPPORTED"
	case errors.Is(err, ports.ErrChatDriverUnavailable), errors.Is(err, ports.ErrAgentBinaryNotFound):
		return "TARGET_UNAVAILABLE"
	case errors.Is(err, ports.ErrChatDriverIncompatible):
		return "TARGET_INCOMPATIBLE"
	case errors.Is(err, ports.ErrChatAuthRequired), errors.Is(err, ports.ErrAgentAuthRequired):
		return "TARGET_AUTH_REQUIRED"
	case errors.Is(err, ErrInterfaceProviderHistoryRecoveryUnavailable):
		return "PROVIDER_HISTORY_RECOVERY_UNAVAILABLE"
	case errors.Is(err, ErrNativeConversationMissing):
		return "NATIVE_SESSION_MISSING"
	case errors.Is(err, ErrNativeConversationUnverified):
		return "NATIVE_SESSION_UNVERIFIED"
	default:
		return "TARGET_PREFLIGHT_FAILED"
	}
}

// transitionDetail words a failure for the user. An account move keeps its
// mode, so the shared texts about interfaces, and the "Cancel request and
// switch" action the account switch does not offer, are replaced.
func transitionDetail(transition domain.SessionInterfaceTransition, code, detail string) string {
	if !transition.AccountMove() {
		return detail
	}
	switch code {
	case "DRAIN_QUIESCENCE_UNVERIFIED":
		return "Tallpine could not confirm that the terminal was idle, so the agent stays on its current account. Try again when it is idle."
	case "DRAIN_DECISION_PENDING":
		return "The agent is waiting for an answer in the terminal, so it stays on its current account. Answer it, then try again."
	case "TRANSITION_CANCELLED":
		return "The account switch was cancelled. The agent stays on its current account."
	case "SESSION_CHANGED":
		return "The session changed before the account switch could start. Try again."
	}
	return detail
}

func (m *Manager) turnFallbackHandoffGeneration(ctx context.Context, rec domain.SessionRecord, transitionID string) (string, error) {
	if store, ok := m.store.(interface {
		TurnFallbackHandoffGeneration(context.Context, domain.SessionID, domain.SessionControllerOwner, string) (string, error)
	}); ok {
		return store.TurnFallbackHandoffGeneration(ctx, rec.ID, rec.ControllerOwner(), transitionID)
	}
	return "", nil
}
