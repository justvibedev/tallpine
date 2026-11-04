package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const nativeEditHandoffLimit = 45 * time.Second

// Conversation history operations: rollback, fork, and the thread title.
//
// The three share one property that shapes everything here: they change the
// provider's own record of the conversation, not Tallpine's view of it. Rollback is the
// sharp one — it changes what the agent REMEMBERS — so Tallpine's durable rows have to
// follow, or the timeline goes on showing prose the agent cannot recall.
//
// Each is optional on the driver and feature-detected, following the Models
// precedent: a provider that cannot do one of these gets a typed refusal the client
// can render as an absent affordance rather than a failed request.

// Typed outcomes for the history operations. Each one distinguishes a permanent
// answer from something worth retrying, because "this agent cannot undo" and "stop
// the agent first" ask completely different things of the person reading it.
var (
	// ErrRollbackUnsupported reports a provider that cannot discard history.
	ErrRollbackUnsupported = errors.New("chat driver cannot roll back history")
	// ErrForkUnsupported reports a provider that cannot branch a conversation.
	ErrForkUnsupported = errors.New("chat driver cannot fork a conversation")
	// ErrRenameUnsupported reports a provider whose thread carries no title.
	ErrRenameUnsupported = errors.New("chat driver cannot set a thread title")
	// ErrOrchestratorRename refuses a title for the project orchestrator's
	// conversation: the orchestrator is never renamed.
	ErrOrchestratorRename = errors.New("the project orchestrator cannot be renamed")
	// ErrTurnRunning refuses a rollback while the agent is working. Retryable once
	// the turn ends, which is why it is separate from every other refusal here.
	ErrTurnRunning = errors.New("cannot roll back while a turn is running")
	// ErrTurnNotRollbackable reports a turn the provider never accepted. There is
	// no provider history to discard, so an undo would only hide Tallpine's own rows and
	// leave the agent remembering more than the timeline shows.
	ErrTurnNotRollbackable = errors.New("turn was never dispatched to the provider")
	// ErrTurnProviderMismatch reports a durable turn owned by an earlier agent
	// provider. Its opaque provider turn id is invalid in the current controller.
	ErrTurnProviderMismatch = errors.New("conversation turn belongs to a different agent provider")
	// ErrTitleRequired refuses a blank thread title. The provider refuses one too,
	// and reporting it here keeps the error about the caller's input.
	ErrTitleRequired = errors.New("thread title is required")
	// ErrProviderRefused reports something the provider declined on its own terms
	// while the conversation stayed healthy. It is a conflict, never an internal
	// failure: the message it carries is the provider's own explanation.
	ErrProviderRefused = errors.New("the agent refused the request")
	// ErrEditTurnInvalid reports a prompt that cannot be safely reconstructed from
	// durable history, including malformed legacy structured content.
	ErrEditTurnInvalid = errors.New("conversation turn cannot be edited")
	// ErrEditDeliveryUncertain means Tallpine reserved the edit handle but cannot prove
	// whether provider delivery and the durable branch result committed together.
	ErrEditDeliveryUncertain = errors.New("edit delivery is uncertain")
	// ErrEditIdempotencyConflict refuses reuse of one edit handle for a different
	// source prompt or replacement payload.
	ErrEditIdempotencyConflict = errors.New("edit idempotency handle belongs to a different request")
	// ErrEditDeliveryRejected reports a durably rejected generic failure, including
	// local preparation failures that prove the replacement prompt was not sent.
	// Ambiguous failures after provider dispatch remain reserved.
	ErrEditDeliveryRejected = errors.New("edit delivery was rejected")
	// ErrBranchProviderMismatch refuses a historical branch whose opaque provider
	// conversation id belongs to an earlier agent ownership epoch.
	ErrBranchProviderMismatch = errors.New("conversation branch belongs to a different agent provider")
)

// providerRefusal is satisfied by driver errors that mean "the provider said no"
// rather than "the call did not get through".
//
// Declared here and satisfied structurally, so this package needs no import of any
// adapter and no adapter needs to know this package's vocabulary. Without the
// distinction every ordinary refusal would surface as a 500.
type providerRefusal interface {
	ChatRefusal() bool
}

// classify folds a driver error into the typed vocabulary above.
func classify(err error) error {
	if err == nil {
		return nil
	}
	var refusal providerRefusal
	if errors.As(err, &refusal) && refusal.ChatRefusal() {
		return fmt.Errorf("%w: %w", ErrProviderRefused, err)
	}
	return err
}

// maxTitleRunes bounds a thread title, matching the automatic-semantic-task-titles
// design's contract. Longer output is treated as generation failure rather than
// truncated into something that reads like a sentence someone cut off.
const maxTitleRunes = 80

// Rollback discards a turn and everything after it, from the agent's memory and
// from Tallpine's timeline.
//
// Refused while a turn is running, and refused BEFORE the provider is asked. The
// provider refuses this too, but relying on that alone would make the outcome a
// race: Tallpine would have already decided to hide rows by the time it learned it could
// not. The controller holds its dispatch lock across the check and the call, so
// within Tallpine the answer cannot change underneath.
func (s *Service) Rollback(ctx context.Context, id domain.SessionID, turnID string) (int, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return 0, err
	}
	defer release()
	if _, ok := controller.conv.(ports.ChatRollbacker); !ok {
		return 0, ErrRollbackUnsupported
	}
	return controller.Rollback(ctx, turnID)
}

// ForkConversation branches this session's provider conversation and returns the
// new provider handle.
//
// Deliberately not reachable over HTTP yet, and the reason is not effort. Tallpine's
// schema allows one conversation per session, so a fork has to become a second Tallpine
// session — and a second session gets a fresh worktree, while the provider's own
// documentation is explicit that a fork copies conversation history and does NOT
// revert or copy the file changes the agent made. The forked agent would remember
// editing files that are not in its tree, which is a worse failure than the absent
// feature: it would look like it worked.
//
// Landing a fork therefore needs the spawn path to adopt the source session's
// worktree state, which is a workspace decision and not a chat one. Until then this
// exists so the provider call is written, tested, and honest about what it returns,
// rather than half-wired into a UI that cannot be correct.
func (s *Service) ForkConversation(ctx context.Context, id domain.SessionID) (string, error) {
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return "", err
	}
	defer release()
	forker, ok := controller.conv.(ports.ChatForker)
	if !ok {
		return "", ErrForkUnsupported
	}
	forked, err := forker.Fork(ctx, nil)
	if err != nil {
		return "", classify(fmt.Errorf("fork conversation for %s: %w", id, err))
	}
	return forked, nil
}
