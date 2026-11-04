package daemon

import (
	"context"
	"errors"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// chatLauncher adapts the chat service to session_manager.ChatLauncher.
//
// The two packages define their own request/result types on purpose so neither
// depends on the other's; this is the one place that knows both, which keeps the
// translation in the wiring rather than in either domain.
type chatLauncher struct {
	svc *chatsvc.Service
	// policy decides the agent policy for reviewer conversations; nil restricts nothing.
	policy                      sessionmanager.PolicyEngine
	persistentHostReconcileDone <-chan struct{}
}

func (c chatLauncher) waitForPersistentHostReconcile(ctx context.Context) error {
	if c.persistentHostReconcileDone == nil {
		return nil
	}
	select {
	case <-c.persistentHostReconcileDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ interface {
	HibernateChat(context.Context, domain.SessionID) (bool, error)
} = chatLauncher{}

var _ sessionmanager.ChatLauncher = chatLauncher{}

// Session Manager asserts these optional capabilities at runtime: a missing
// forward compiles but silently turns the feature off.
var _ sessionmanager.ChatVersionRefresher = chatLauncher{}
var _ sessionmanager.ChatUpdateHibernator = chatLauncher{}
var _ interface {
	RunBackgroundTask(context.Context, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
	RunPromptSuggestionTask(context.Context, domain.SessionID, string, domain.AgentHarness, ports.ChatStartConfig, string) (string, error)
	ArmChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	PrepareChatHandoff(context.Context, domain.SessionID, domain.SessionInterfaceTransitionPolicy) error
	AbortChatHandoff(domain.SessionID)
} = chatLauncher{}

func (c chatLauncher) SupportsChat(harness domain.AgentHarness) bool {
	return c.svc.SupportsChat(harness)
}

func (c chatLauncher) PreflightChat(
	ctx context.Context,
	harness domain.AgentHarness,
	permissions ports.PermissionMode,
	decision agentpolicy.Decision,
) error {
	return c.svc.PreflightChat(ctx, harness, permissions, decision)
}

// decideReview is the policy decision for a reviewer conversation of project.
func (c chatLauncher) decideReview(ctx context.Context, project domain.ProjectID, harness domain.AgentHarness) agentpolicy.Decision {
	if c.policy == nil {
		return agentpolicy.Decision{}
	}
	return c.policy.Decide(ctx, agentpolicy.SessionRef{Project: project, Kind: domain.KindWorker, Harness: harness})
}

func (c chatLauncher) SupportsReviewChat(harness domain.AgentHarness) bool {
	return c.svc.SupportsChat(harness)
}

func (c chatLauncher) PreflightReviewChat(ctx context.Context, harness domain.AgentHarness) error {
	return c.svc.PreflightChat(ctx, harness, ports.PermissionModeAuto, c.decideReview(ctx, "", harness))
}

func (c chatLauncher) StartReviewChat(ctx context.Context, cfg reviewcore.ReviewerChatStart) (string, error) {
	return c.startReviewChat(ctx, cfg, true)
}

func (c chatLauncher) RestoreReviewChat(ctx context.Context, cfg reviewcore.ReviewerChatStart) (string, error) {
	return c.startReviewChat(ctx, cfg, false)
}

func (c chatLauncher) startReviewChat(ctx context.Context, cfg reviewcore.ReviewerChatStart, sendPrompt bool) (string, error) {
	if err := c.waitForPersistentHostReconcile(ctx); err != nil {
		return "", err
	}
	owner := domain.ReviewConversationOwner(cfg.ReviewID)
	started, err := c.svc.StartChat(ctx, chatsvc.StartConfig{Policy: c.decideReview(ctx, cfg.ProjectID, cfg.Harness), Owner: owner, SessionID: cfg.WorkerID, ProjectID: cfg.ProjectID, Kind: domain.KindWorker, Harness: cfg.Harness, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath, Env: cfg.Env, Model: cfg.Model, Effort: cfg.Effort, Permissions: ports.PermissionModeAuto, SystemPrompt: cfg.SystemPrompt, ProviderConversationID: cfg.ProviderConversationID})
	if err != nil {
		return "", err
	}
	if !sendPrompt || strings.TrimSpace(cfg.Prompt) == "" {
		return started.ProviderConversationID, nil
	}
	if _, err := c.svc.SendForOwner(ctx, owner, ports.ChatUserMessage{Text: cfg.Prompt, Origin: domain.MessageOriginDaemon, ClientMessageID: reviewcore.BatchMessageID(cfg.BatchID)}); err != nil {
		_ = c.svc.StopForOwner(context.Background(), owner)
		return "", err
	}
	return started.ProviderConversationID, nil
}

func (c chatLauncher) SendReviewChat(ctx context.Context, reviewID, message, batchID string) error {
	_, err := c.svc.SendForOwner(ctx, domain.ReviewConversationOwner(reviewID), ports.ChatUserMessage{Text: message, Origin: domain.MessageOriginDaemon, ClientMessageID: reviewcore.BatchMessageID(batchID)})
	return err
}

func (c chatLauncher) ReviewChatAlive(reviewID string) bool {
	return c.svc.HasLiveControllerForOwner(domain.ReviewConversationOwner(reviewID))
}

func (c chatLauncher) InterruptReviewChat(ctx context.Context, reviewID string) error {
	err := c.svc.InterruptForOwner(ctx, domain.ReviewConversationOwner(reviewID))
	if errors.Is(err, chatsvc.ErrNoActiveTurn) || errors.Is(err, ports.ErrChatNoActiveTurn) {
		return nil
	}
	return err
}

func (c chatLauncher) StopReviewChat(ctx context.Context, reviewID string) error {
	return c.svc.StopForOwner(ctx, domain.ReviewConversationOwner(reviewID))
}

func (c chatLauncher) StartChat(ctx context.Context, cfg sessionmanager.ChatStart) (sessionmanager.ChatStarted, error) {
	if err := c.waitForPersistentHostReconcile(ctx); err != nil {
		return sessionmanager.ChatStarted{}, err
	}
	return c.svc.StartChat(ctx, cfg)
}

func (c chatLauncher) RunBackgroundTask(ctx context.Context, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (string, error) {
	return c.svc.RunBackgroundTask(ctx, harness, cfg, prompt)
}

func (c chatLauncher) RunPromptSuggestionTask(ctx context.Context, id domain.SessionID, turnID string, harness domain.AgentHarness, cfg ports.ChatStartConfig, prompt string) (string, error) {
	return c.svc.RunPromptSuggestionTask(ctx, id, turnID, harness, cfg, prompt)
}

func (c chatLauncher) StartChatTurn(ctx context.Context, id domain.SessionID, text string) (string, error) {
	return c.svc.StartChatTurn(ctx, id, text)
}

func (c chatLauncher) RelaySessionChatTurnReceipt(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error) {
	return c.svc.RelaySessionChatTurnReceipt(ctx, id, text, clientMessageID, options)
}

func (c chatLauncher) QueueRelayForResume(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (ports.MessageReceipt, error) {
	return c.svc.QueueRelayForResume(ctx, id, text, clientMessageID, options)
}

func (c chatLauncher) RelaySessionChatTurn(ctx context.Context, id domain.SessionID, text, clientMessageID string, options ports.MessageDeliveryOptions) (string, error) {
	return c.svc.RelaySessionChatTurn(ctx, id, text, clientMessageID, options)
}

func (c chatLauncher) QueueChatPrompt(ctx context.Context, id domain.SessionID, text string) (string, error) {
	turn, err := c.svc.QueueUserMessage(ctx, id, ports.ChatUserMessage{
		Text: text,
		// The opening prompt is the user's own task brief, whatever carries it.
		Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		return "", err
	}
	return turn.ID, nil
}

// PostSessionNotice lets the session manager announce a model fallback on the
// timeline before the chat starts.
func (c chatLauncher) PostSessionNotice(ctx context.Context, id domain.SessionID, summary string) (bool, error) {
	return c.svc.PostSessionNotice(ctx, id, summary)
}

func (c chatLauncher) DrainChatQueue(ctx context.Context, id domain.SessionID) error {
	return c.svc.DrainQueued(ctx, id)
}

func (c chatLauncher) HasLiveChatController(id domain.SessionID) bool {
	return c.svc.HasLiveChatController(id)
}

func (c chatLauncher) HibernateChat(ctx context.Context, id domain.SessionID) (bool, error) {
	return c.svc.HibernateChat(ctx, id)
}

func (c chatLauncher) SwitchToInstalledRelease(ctx context.Context, id domain.SessionID, minIdle time.Duration) (bool, error) {
	return c.svc.SwitchToInstalledRelease(ctx, id, minIdle)
}

func (c chatLauncher) HibernateChatForUpdate(ctx context.Context, id domain.SessionID) (bool, error) {
	return c.svc.HibernateChatForUpdate(ctx, id)
}

// WaitForWake lets a version move hold its restart permit until the provider
// it stopped has started again; without it the manager skips the wait.
func (c chatLauncher) WaitForWake(ctx context.Context, id domain.SessionID) error {
	return c.svc.WaitForWake(ctx, id)
}

// The manager discovers these optional capabilities by type assertion, so a
// method the wrapper forgets to forward is lost silently.
var (
	_ sessionmanager.ChatWakeWaiter       = chatLauncher{}
	_ sessionmanager.ChatVersionRefresher = chatLauncher{}
	_ sessionmanager.ChatUpdateHibernator = chatLauncher{}
)

// ArmChatHandoff closes Chat intake and dispatch synchronously at transition
// acceptance. PrepareChatHandoff then settles interrupt work or waits for drain
// work before Session Manager stops the source. These methods intentionally live
// on the wiring adapter: Session Manager's handoff capability is optional, but
// wrapping the concrete Chat service must not erase it.
func (c chatLauncher) ArmChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	return c.svc.ArmChatHandoff(ctx, id, policy)
}

// MarkAccountSwitch marks the turn an account move stopped, so the chat shows
// the switch instead of a failure.
func (c chatLauncher) MarkAccountSwitch(ctx context.Context, id domain.SessionID, accountID string) error {
	return c.svc.MarkAccountSwitch(ctx, id, accountID)
}

func (c chatLauncher) PrepareChatHandoff(
	ctx context.Context,
	id domain.SessionID,
	policy domain.SessionInterfaceTransitionPolicy,
) error {
	return c.svc.PrepareChatHandoff(ctx, id, policy)
}

func (c chatLauncher) AbortChatHandoff(id domain.SessionID) {
	c.svc.AbortChatHandoff(id)
}

func (c chatLauncher) StopChat(ctx context.Context, id domain.SessionID) error {
	return c.svc.StopChat(ctx, id)
}
