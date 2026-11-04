package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Steering: guidance delivered into a turn that is already running.
//
// Why this is not "interrupt, then send again". An interrupt destroys the turn —
// its reasoning, its half-applied edits, the command it has running — and the
// resend starts from nothing. A steer adds the correction to the SAME turn and
// leaves the work in place; the agent decides what to abandon. Measured against
// codex-cli 0.146.0, the steered turn kept its id and settled `completed`, having
// abandoned the command it was running on its own. So the user's guidance costs
// them nothing they had already paid for, which is the entire reason to prefer it.
//
// Optional and feature-detected, following the Models precedent: a provider that
// cannot steer gets a typed refusal a client renders as an absent control rather
// than as a failed request.

// Typed outcomes for steering. Each distinguishes advice a client can act on:
// "this agent cannot do it", "send it as a new message instead", "wait, then try
// again".
var (
	// ErrSteerUnsupported reports a driver whose provider cannot take guidance into
	// a running turn. Permanent for that harness — a client should stop offering the
	// control rather than retry.
	ErrSteerUnsupported = errors.New("chat driver cannot steer a running turn")
	// ErrTurnNotSteerable reports a turn that is running but cannot absorb guidance.
	// Codex refuses a compaction or review turn this way. Retryable once that turn
	// finishes, which is what separates it from ErrSteerUnsupported.
	ErrTurnNotSteerable = errors.New("the running turn cannot take guidance")
	// ErrSteerTextRequired refuses an empty steer. There is no keystroke concept in
	// Chat mode: an empty body is a caller bug, not a way to nudge the agent.
	ErrSteerTextRequired = errors.New("steer text is required")
	// ErrTurnNotQueued reports a selected source that has already dispatched,
	// settled, or been claimed by another promotion.
	ErrTurnNotQueued = errors.New("turn is not queued")
	// ErrPromotionUncertain prevents automatic redelivery after the provider may
	// have accepted guidance but Tallpine could not durably record the result.
	ErrPromotionUncertain = errors.New("queued turn promotion delivery is uncertain")
	// ErrSteerDeliveryUncertain means Tallpine reserved an idempotency handle but cannot
	// prove whether the provider accepted it. Reusing the handle is safe and must
	// never contact the provider again; inventing a fresh handle is not.
	ErrSteerDeliveryUncertain = errors.New("steer delivery is uncertain")
	// errSteerProviderCallFailed marks a steer whose provider call itself failed
	// without a definitive refusal: the guidance may have reached the agent.
	// Errors without it (or ErrSteerDeliveryUncertain) happened before dispatch.
	errSteerProviderCallFailed = errors.New("steer provider call failed")
	// ErrSteerIdempotencyConflict refuses reuse of one handle for different
	// guidance. Dispatching either request would make the handle lie about identity.
	ErrSteerIdempotencyConflict = errors.New("steer idempotency handle belongs to different guidance")
	// ErrSteerContentUnsupported means the provider cannot accept every structured
	// block. The whole queued message stays undelivered.
	ErrSteerContentUnsupported = errors.New("steer content is unsupported")
)

// SteerResult is what a steer did, for a client that has to attribute it.
type SteerResult struct {
	// ProviderTurnID is the turn that absorbed the guidance. Reported rather than
	// assumed to be the one asked for: the provider names it in its own response,
	// and a client matches it against the snapshot's turns.
	ProviderTurnID string
	// ActivityID is the timeline row recording the guidance, so a client can
	// reconcile an optimistic bubble with the durable one instead of showing both.
	ActivityID string
}

// SteerOrSendResult identifies whether one atomic request joined an active turn
// or opened a normal turn while the conversation was idle.
type SteerOrSendResult struct {
	Steered   bool
	Duplicate bool
	// Cleared reports a /clear Tallpine carried out: no turn was sent, and
	// Steer.ActivityID is the transcript marker.
	Cleared bool
	Steer   SteerResult
	Turn    domain.ConversationTurn
}

// resolveSteerSender enriches CLI-originated steering with the source session's
// current durable identity. A missing source is tolerated so older workers and
// cross-daemon callers keep the raw text prefix as their fallback.
func (s *Service) resolveSteerSender(ctx context.Context, msg ports.ChatUserMessage) ports.ChatUserMessage {
	if msg.SenderSessionID == "" || s.sessions == nil {
		return msg
	}
	record, found, err := s.sessions.GetSession(ctx, domain.SessionID(msg.SenderSessionID))
	if err != nil || !found {
		return msg
	}
	msg.SenderProjectID = string(record.ProjectID)
	msg.SenderDisplayName = strings.TrimSpace(record.DisplayName)
	return msg
}

// PromoteQueuedTurnResult attributes a durable queue item to the running turn
// that absorbed it.
type PromoteQueuedTurnResult struct {
	SourceTurnID   string
	ProviderTurnID string
	ActivityID     string
}

// maxSteerSummaryRunes bounds the one-line label a steer lands on the timeline
// with. The full text is always kept in the activity's detail payload; this is only
// what a collapsed row shows.
const maxSteerSummaryRunes = 120

// Steer sends guidance into this session's running turn.
//
// Dispatch is decided from the persisted session mode like every other Chat
// command, so a TUI session cannot reach this path by calling the URL directly.
func (s *Service) Steer(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
) (SteerResult, error) {
	if strings.TrimSpace(msg.Text) == "" {
		return SteerResult{}, ErrSteerTextRequired
	}
	if isClearCommand(msg) {
		record, err := s.requireChatSession(ctx, id)
		if err != nil {
			return SteerResult{}, err
		}
		if interceptsClear(record, msg) {
			marker, err := s.clearContext(ctx, id)
			return SteerResult{ActivityID: marker}, err
		}
	}
	msg = s.resolveSteerSender(ctx, msg)
	if msg.SenderSessionID != "" {
		msg.Origin = domain.MessageOriginAutomation
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return SteerResult{}, err
	}
	defer release()
	return controller.Steer(ctx, msg)
}

// RecoverSteer reads only the durable receipt. A missing controller or a changed
// interface cannot turn an earlier accepted/uncertain delivery into a rejection.
func (s *Service) RecoverSteer(ctx context.Context, id domain.SessionID, clientMessageID string) (SteerResult, error) {
	if clientMessageID == "" {
		return SteerResult{}, ErrSteerDeliveryUncertain
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: load conversation: %w", ErrSteerDeliveryUncertain, err)
	}
	delivery, found, err := s.store.SteerDelivery(ctx, conversation.ID, clientMessageID)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: load receipt: %w", ErrSteerDeliveryUncertain, err)
	}
	if !found {
		return SteerResult{}, ErrSteerDeliveryUncertain
	}
	return replaySteerDelivery(delivery, delivery.RequestJSON)
}

// SteerOrSend routes one idempotent request through the session's live Chat
// controller without exposing a state-check race to the caller.
func (s *Service) SteerOrSend(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
	recoverOnly bool,
) (SteerOrSendResult, error) {
	if strings.TrimSpace(msg.Text) == "" && !recoverOnly {
		return SteerOrSendResult{}, ErrSteerTextRequired
	}
	if msg.ClientMessageID == "" {
		return SteerOrSendResult{}, ErrSteerDeliveryUncertain
	}
	if isClearCommand(msg) {
		record, err := s.requireChatSession(ctx, id)
		if err != nil {
			return SteerOrSendResult{}, err
		}
		if interceptsClear(record, msg) {
			marker, err := s.clearContext(ctx, id)
			return SteerOrSendResult{Cleared: err == nil, Steer: SteerResult{ActivityID: marker}}, err
		}
	}
	msg = s.resolveSteerSender(ctx, msg)
	// A cross-session steer that finds no active turn falls through to the normal
	// send path. Keep that message attributed as automation so idle and busy
	// targets render the same way.
	if msg.SenderSessionID != "" {
		msg.Origin = domain.MessageOriginAutomation
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return SteerOrSendResult{}, err
	}
	defer release()
	return controller.SteerOrSend(ctx, msg, recoverOnly)
}

// PromoteQueuedTurn delivers one already queued turn into the active turn. The
// daemon reads the queued content; callers identify it but cannot replace it.
func (s *Service) PromoteQueuedTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
) (PromoteQueuedTurnResult, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	if _, ok := controller.steerer(); !ok {
		return PromoteQueuedTurnResult{}, ErrSteerUnsupported
	}
	return controller.PromoteQueuedTurn(ctx, turnID)
}

// steerer returns the provider's steering port only when the conversation
// negotiated it. The ACP driver implements Steer for every agent, so the type
// alone does not say the agent can take guidance mid-turn.
func (c *Controller) steerer() (ports.ChatSteerer, bool) {
	steerer, ok := c.conv.(ports.ChatSteerer)
	return steerer, ok && c.Capabilities().Has(ports.ChatCapabilitySteer)
}
