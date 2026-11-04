package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Snapshot is the durable read model a client bootstraps from.
type Snapshot struct {
	Conversation                     domain.ConversationRecord
	ActiveBranch                     domain.ConversationBranch
	EditFloorSequence                int64
	NativeForkAvailableAfterSequence int64
	SessionID                        domain.SessionID
	Harness                          domain.AgentHarness
	Mode                             domain.SessionMode
	Controller                       ports.ChatControllerState
	Turns                            []domain.ConversationTurn
	Messages                         []domain.ConversationMessage
	Activities                       []domain.ConversationActivity
	BranchPoints                     []domain.ConversationBranchPoint
	BranchedFromEarlierMessage       bool
	OldestSequence                   int64
	HasMoreBefore                    bool
	// Usage and RateLimits are current state carried on the snapshot the client
	// already polls, rather than timeline entries or a second request. Both are nil
	// until the provider has reported, so a client can tell "not known yet" from a
	// real zero.
	Usage      *domain.ConversationUsage
	RateLimits *domain.ConversationRateLimits
	// Capabilities is what this session's provider can actually do, so a client can
	// decide what to offer BEFORE offering it. Without this the only way to find out
	// is to try: a session whose harness cannot steer would still draw "Steer this
	// turn", take the press, and withdraw the control on the refusal — which reads
	// as a bug rather than as a harness difference. That matters as soon as a second
	// driver lands, and the abilities already differ per install today. Nil when no
	// controller is live, because an unstarted session's abilities are not yet known
	// and guessing them is how a control appears and then vanishes.
	Capabilities ports.ChatCapabilities
}

// SnapshotReader is the durable read the service serves snapshots from. Kept
// separate from Store so the write path and the read path can be satisfied
// independently.
type SnapshotReader interface {
	LoadConversationSnapshot(ctx context.Context, conversationID string) (ConversationRows, error)
}

// SnapshotPageReader is the production bounded-history path. It is separate from
// SnapshotReader so existing tests and recovery tools can still request a full
// snapshot explicitly.
type SnapshotPageReader interface {
	LoadConversationSnapshotPage(ctx context.Context, conversationID string, beforeSequence, limit int64) (ConversationRows, error)
}

// ConversationRows is the raw durable read.
type ConversationRows struct {
	Conversation                     domain.ConversationRecord
	ActiveBranch                     domain.ConversationBranch
	EditFloorSequence                int64
	NativeForkAvailableAfterSequence int64
	Turns                            []domain.ConversationTurn
	Messages                         []domain.ConversationMessage
	Activities                       []domain.ConversationActivity
	BranchPoints                     []domain.ConversationBranchPoint
	BranchedFromEarlierMessage       bool
	OldestSequence                   int64
	HasMoreBefore                    bool
}

// idleControllerState is what a session with no live controller reports. A
// session that is still starting is connecting, not stopped: nothing has
// stopped, and a client that reads "stopped" hides the composer on a session
// the user is meant to keep typing into.
func idleControllerState(record domain.SessionRecord) ports.ChatControllerState {
	if record.IsTerminated {
		return ports.ChatControllerStopped
	}
	if record.ProvisionState.IsProvisioning() {
		return ports.ChatControllerConnecting
	}
	if record.HibernatedAt != nil {
		return ports.ChatControllerHibernated
	}
	return ports.ChatControllerStopped
}

// withDispatchingTurnRunning reports the turn being dispatched as running. Its row
// stays queued until the provider binds it, but a client reads queued as "waiting
// behind other work", which is false for a message sent to an idle agent.
func withDispatchingTurnRunning(turns []domain.ConversationTurn, dispatching ...string) []domain.ConversationTurn {
	var out []domain.ConversationTurn
	for i := range turns {
		if turns[i].State != domain.TurnStateQueued || !slices.Contains(dispatching, turns[i].ID) {
			continue
		}
		if out == nil {
			out = append([]domain.ConversationTurn(nil), turns...)
		}
		out[i].State = domain.TurnStateRunning
	}
	if out == nil {
		return turns
	}
	return out
}

// Snapshot reads a session's conversation.
//
// It does not require a live controller: history must remain readable after the
// agent process is gone, which is the whole point of persisting it. The
// controller state is reported separately so the client can distinguish "no
// history" from "agent not running".
func (s *Service) Snapshot(ctx context.Context, id domain.SessionID) (Snapshot, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}

	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		// A chat session has no conversation until its controller first starts.
		// That is an empty conversation, not a failure — returning an error here
		// would make a brand-new session look broken.
		return Snapshot{
			SessionID:  id,
			Harness:    record.Harness,
			Mode:       domain.NormalizeSessionMode(record.Mode),
			Controller: idleControllerState(record),
		}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("conversation for %s: %w", id, err)
	}

	rows, err := s.reader.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load conversation %s: %w", conversation.ID, err)
	}

	waking := s.isWaking(id)
	state := idleControllerState(record)
	if waking && !record.IsTerminated && state == ports.ChatControllerStopped {
		state = ports.ChatControllerHibernated
	}
	var caps ports.ChatCapabilities
	var dispatching []string
	if controller, err := s.Controller(id); err == nil {
		dispatching = controller.DispatchingTurnIDs()
		if live := controller.State(); (record.HibernatedAt == nil && !waking) || live != ports.ChatControllerStopped {
			state = live
			caps = controller.Capabilities()
		}
		// Fast and plan mode live with the controller, not in the stored row.
		live := controller.Settings()
		rows.Conversation.Settings.FastMode, rows.Conversation.Settings.PlanMode = live.FastMode, live.PlanMode
		s.overlayCompactionView(ctx, &rows.Conversation.Settings, controller, s.compactionPending(ctx, controller))
	} else {
		s.overlayResolvedView(ctx, &rows.Conversation.Settings, record.Harness, record.Kind, rows.Conversation.ProjectID)
	}

	return Snapshot{
		Conversation:                     rows.Conversation,
		ActiveBranch:                     rows.ActiveBranch,
		EditFloorSequence:                rows.EditFloorSequence,
		NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
		SessionID:                        id,
		Harness:                          record.Harness,
		Mode:                             domain.NormalizeSessionMode(record.Mode),
		Controller:                       state,
		Turns:                            withDispatchingTurnRunning(rows.Turns, dispatching...),
		Messages:                         rows.Messages,
		Activities:                       rows.Activities,
		BranchPoints:                     rows.BranchPoints,
		BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
		Capabilities:                     caps,
		Usage:                            rows.Conversation.Usage,
		RateLimits:                       rows.Conversation.RateLimits,
	}, nil
}

// SnapshotForReview reads a reviewer-owned chat narrative without treating the
// reviewer row as a Tallpine session.
func (s *Service) SnapshotForReview(ctx context.Context, reviewID string) (Snapshot, error) {
	store, ok := s.store.(reviewerConversationStore)
	if !ok {
		return Snapshot{}, errors.New("reviewer conversation store is unavailable")
	}
	review, found, err := store.GetReviewByID(ctx, reviewID)
	if err != nil {
		return Snapshot{}, err
	}
	if !found {
		return Snapshot{}, ports.ErrSessionNotFound
	}
	if review.InterfaceMode != domain.ReviewerInterfaceChat {
		return Snapshot{}, ErrNotChatMode
	}
	conversation, err := store.ConversationForReview(ctx, reviewID)
	if errors.Is(err, domain.ErrNoConversation) {
		return Snapshot{SessionID: review.SessionID, Harness: domain.AgentHarness(review.Harness), Mode: domain.SessionModeChat, Controller: ports.ChatControllerStopped}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	rows, err := s.reader.LoadConversationSnapshot(ctx, conversation.ID)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshotForReviewRows(review, rows), nil
}

func (s *Service) snapshotForReviewRows(review domain.Review, rows ConversationRows) Snapshot {
	state := ports.ChatControllerStopped
	var caps ports.ChatCapabilities
	var dispatching []string
	if controller, err := s.ControllerForOwner(domain.ReviewConversationOwner(review.ID)); err == nil {
		state, caps = controller.State(), controller.Capabilities()
		dispatching = controller.DispatchingTurnIDs()
	}
	return Snapshot{Conversation: rows.Conversation, ActiveBranch: rows.ActiveBranch, EditFloorSequence: rows.EditFloorSequence, NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence, SessionID: review.SessionID, Harness: domain.AgentHarness(review.Harness), Mode: domain.SessionModeChat, Controller: state, Turns: withDispatchingTurnRunning(rows.Turns, dispatching...), Messages: rows.Messages, Activities: rows.Activities, BranchPoints: rows.BranchPoints, BranchedFromEarlierMessage: rows.BranchedFromEarlierMessage, OldestSequence: rows.OldestSequence, HasMoreBefore: rows.HasMoreBefore, Capabilities: caps, Usage: rows.Conversation.Usage, RateLimits: rows.Conversation.RateLimits}
}

// SnapshotPage reads one bounded timeline page. The live conversation metadata
// remains current on every page; only turns/messages/activities are windowed.
func (s *Service) SnapshotPage(ctx context.Context, id domain.SessionID, beforeSequence, limit int64) (Snapshot, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return Snapshot{
			SessionID:  id,
			Harness:    record.Harness,
			Mode:       domain.NormalizeSessionMode(record.Mode),
			Controller: idleControllerState(record),
		}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("conversation for %s: %w", id, err)
	}
	if s.pageReader == nil {
		// A non-production reader (normally a focused test fake) can retain the
		// old full read. Production always wires PageReader.
		return s.Snapshot(ctx, id)
	}
	rows, err := s.pageReader.LoadConversationSnapshotPage(ctx, conversation.ID, beforeSequence, limit)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load conversation page %s: %w", conversation.ID, err)
	}
	waking := s.isWaking(id)
	state := idleControllerState(record)
	if waking && !record.IsTerminated && state == ports.ChatControllerStopped {
		state = ports.ChatControllerHibernated
	}
	var caps ports.ChatCapabilities
	var dispatching []string
	if controller, err := s.Controller(id); err == nil {
		dispatching = controller.DispatchingTurnIDs()
		if live := controller.State(); (record.HibernatedAt == nil && !waking) || live != ports.ChatControllerStopped {
			state = live
			caps = controller.Capabilities()
		}
		// Fast and plan mode live with the controller, not in the stored row.
		live := controller.Settings()
		rows.Conversation.Settings.FastMode, rows.Conversation.Settings.PlanMode = live.FastMode, live.PlanMode
		s.overlayCompactionView(ctx, &rows.Conversation.Settings, controller, s.compactionPending(ctx, controller))
	} else {
		s.overlayResolvedView(ctx, &rows.Conversation.Settings, record.Harness, record.Kind, rows.Conversation.ProjectID)
	}
	return Snapshot{
		Conversation:                     rows.Conversation,
		ActiveBranch:                     rows.ActiveBranch,
		EditFloorSequence:                rows.EditFloorSequence,
		NativeForkAvailableAfterSequence: rows.NativeForkAvailableAfterSequence,
		SessionID:                        id,
		Harness:                          record.Harness,
		Mode:                             domain.NormalizeSessionMode(record.Mode),
		Controller:                       state,
		Turns:                            withDispatchingTurnRunning(rows.Turns, dispatching...),
		Messages:                         rows.Messages,
		Activities:                       rows.Activities,
		BranchPoints:                     rows.BranchPoints,
		BranchedFromEarlierMessage:       rows.BranchedFromEarlierMessage,
		OldestSequence:                   rows.OldestSequence,
		HasMoreBefore:                    rows.HasMoreBefore,
		Capabilities:                     caps,
		Usage:                            rows.Conversation.Usage,
		RateLimits:                       rows.Conversation.RateLimits,
	}, nil
}

// SnapshotPageForReview returns a paginated snapshot of a reviewer-owned chat.
func (s *Service) SnapshotPageForReview(ctx context.Context, reviewID string, beforeSequence, limit int64) (Snapshot, error) {
	if s.pageReader == nil {
		return s.SnapshotForReview(ctx, reviewID)
	}
	store, ok := s.store.(reviewerConversationStore)
	if !ok {
		return Snapshot{}, errors.New("reviewer conversation store is unavailable")
	}
	review, found, err := store.GetReviewByID(ctx, reviewID)
	if err != nil {
		return Snapshot{}, err
	}
	if !found {
		return Snapshot{}, ports.ErrSessionNotFound
	}
	if review.InterfaceMode != domain.ReviewerInterfaceChat {
		return Snapshot{}, ErrNotChatMode
	}
	conversation, err := store.ConversationForReview(ctx, reviewID)
	if errors.Is(err, domain.ErrNoConversation) {
		return s.SnapshotForReview(ctx, reviewID)
	}
	if err != nil {
		return Snapshot{}, err
	}
	rows, err := s.pageReader.LoadConversationSnapshotPage(ctx, conversation.ID, beforeSequence, limit)
	if err != nil {
		return Snapshot{}, err
	}
	return s.snapshotForReviewRows(review, rows), nil
}

// SnapshotReaderFunc adapts a plain function to SnapshotReader. The daemon wiring
// uses it to convert the store's own snapshot type, so this package never has to
// import the storage layer.
type SnapshotReaderFunc func(ctx context.Context, conversationID string) (ConversationRows, error)

// LoadConversationSnapshot satisfies SnapshotReader.
func (f SnapshotReaderFunc) LoadConversationSnapshot(
	ctx context.Context,
	conversationID string,
) (ConversationRows, error) {
	return f(ctx, conversationID)
}

// SnapshotPageReaderFunc adapts a function to SnapshotPageReader.
type SnapshotPageReaderFunc func(ctx context.Context, conversationID string, beforeSequence, limit int64) (ConversationRows, error)

// LoadConversationSnapshotPage satisfies SnapshotPageReader.
func (f SnapshotPageReaderFunc) LoadConversationSnapshotPage(
	ctx context.Context,
	conversationID string,
	beforeSequence, limit int64,
) (ConversationRows, error) {
	return f(ctx, conversationID, beforeSequence, limit)
}

/* ---- session_manager.ChatLauncher ---------------------------------------- */

// The methods below let the session manager launch a chat controller during spawn
// without importing this package's config types. They are deliberately narrow:
// the manager decides when, this package decides how.
