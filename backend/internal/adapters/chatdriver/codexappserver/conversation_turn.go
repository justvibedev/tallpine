package codexappserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// SendTurn delivers one message to the provider.
func (c *conversation) SendTurn(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatTurnRef, error) {
	if strings.TrimSpace(msg.Text) == "" {
		// There is no keystroke concept here: an empty message is a caller bug,
		// not a way to nudge the agent.
		return ports.ChatTurnRef{}, errors.New("chat message text is empty")
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	params := map[string]any{
		"threadId": c.thread(),
		"input":    []any{map[string]any{"type": "text", "text": msg.Text}},
	}
	if msg.ClientMessageID != "" {
		// The provider's own idempotency handle: a retry carrying the same id
		// must not produce a second turn.
		params["clientUserMessageId"] = msg.ClientMessageID
	}
	applyTurnSettings(params, msg.Settings, c.readOnly, c.disableNetwork, c.policy, c.workspace)
	// A service tier sticks to the thread once set, so leaving the fast tier
	// has to be said: an explicit null returns the thread to its default.
	if msg.Settings.Fast {
		params["serviceTier"] = cmp.Or(c.fastTiers[msg.Settings.Model], c.fastTiers[c.threadModel], codexFastServiceTier)
	} else if c.fastTier || c.tierUnknown && c.configuredServiceTier(ctx) == "" {
		params["serviceTier"] = nil
	}

	// Plan mode is a collaboration mode, sticky too, so leaving it sends the
	// default mode. The mode names its model; with none known nothing is sent.
	planMode := c.planMode
	if model := cmp.Or(msg.Settings.Model, c.threadModel); (msg.Settings.Plan != c.planMode || c.planUnknown) && model != "" {
		mode := "default"
		if msg.Settings.Plan {
			mode = "plan"
		}
		settings := map[string]any{"model": model, "developer_instructions": nil}
		if effort := cmp.Or(msg.Settings.Effort, c.threadEffort); effort != "" {
			settings["reasoning_effort"] = effort
		}
		params["collaborationMode"] = map[string]any{"mode": mode, "settings": settings}
		planMode = msg.Settings.Plan
	}
	planKnown := params["collaborationMode"] != nil

	var resp struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := c.conn.request(ctx, "turn/start", params, &resp); err != nil {
		return ports.ChatTurnRef{}, fmt.Errorf("turn/start: %w", err)
	}
	c.fastTier = msg.Settings.Fast
	c.planMode = planMode
	c.tierUnknown, c.planUnknown = false, c.planUnknown && !planKnown

	c.mu.Lock()
	c.activeTurn = resp.Turn.ID
	c.mu.Unlock()

	return ports.ChatTurnRef{ProviderTurnID: c.scopedID(resp.Turn.ID)}, nil
}

// applyTurnSettings folds the caller's per-turn choices into a turn/start payload.
//
// Only fields the caller actually chose are sent. An omitted field lets the
// provider fall back to what the thread was started with, which is why a caller
// that chooses nothing behaves exactly as it did before per-turn settings existed.
func applyTurnSettings(params map[string]any, settings ports.ChatTurnSettings, readOnly, disableNetwork bool, decision agentpolicy.Decision, workspace string) {
	if settings.Model != "" {
		params["model"] = settings.Model
	}
	if settings.Effort != "" {
		params["effort"] = settings.Effort
	}
	if settings.Approval != "" {
		// The same posture thread/start applies, so a per-turn choice and a launch
		// choice cannot mean different things. The wire shapes differ though: a
		// thread takes `sandbox: "workspace-write"`, a turn takes a tagged
		// `sandboxPolicy: {type: "workspaceWrite"}`. Sending a thread's shape to a
		// turn is rejected as a missing `type`, so the two are mapped separately
		// rather than assumed to be interchangeable.
		policy, sandbox := approvalSettings(decision, settings.Approval)
		params["approvalPolicy"] = policy
		params["approvalsReviewer"] = approvalReviewer(decision, settings.Approval)
		params["sandboxPolicy"] = turnSandboxPolicy(sandbox, decision.GitRepo(), workspace)
	}
	if readOnly {
		// Reviewers need gh and tallpine review submit; text-only background calls
		// explicitly disable that tool network access.
		params["approvalPolicy"] = "never"
		params["approvalsReviewer"] = "user"
		params["sandboxPolicy"] = map[string]any{"type": "readOnly", "networkAccess": !disableNetwork}
	}
}

// turnSandboxPolicy converts a thread-level sandbox name into the tagged object
// turn/start expects.
func turnSandboxPolicy(sandbox, repo, workspace string) map[string]any {
	switch sandbox {
	case "workspace-write":
		home, _ := os.UserHomeDir()
		network, roots := permissioncap.CodexWorkspaceWriteExtras(home, repo, workspace)
		policy := map[string]any{"type": "workspaceWrite", "networkAccess": network}
		if len(roots) > 0 {
			policy["writableRoots"] = roots
		}
		return policy
	case "read-only":
		return map[string]any{"type": "readOnly"}
	default:
		return map[string]any{"type": "dangerFullAccess"}
	}
}

// Interrupt cancels a turn. An empty turn id targets the active one.
func (c *conversation) Interrupt(ctx context.Context, providerTurnID string) error {
	providerTurnID = c.nativeID(providerTurnID)
	if providerTurnID == "" {
		c.mu.Lock()
		providerTurnID = c.activeTurn
		c.mu.Unlock()
	}
	if providerTurnID == "" {
		return ports.ErrChatNoActiveTurn
	}
	if err := c.conn.request(ctx, "turn/interrupt", map[string]any{
		"threadId": c.thread(),
		"turnId":   providerTurnID,
	}, nil); err != nil {
		// The provider refuses an interrupt for a turn it does not consider
		// active — which happens either side of the turn: pressed before it has
		// acknowledged the start, or after it already finished. Neither is an
		// internal failure, so it is translated here, where the provider's
		// vocabulary is known, instead of escaping as a protocol error and
		// reaching the user as "Internal server error".
		if isNoActiveTurn(err) {
			return ports.ErrChatNoActiveTurn
		}
		return fmt.Errorf("turn/interrupt: %w", err)
	}
	return nil
}

// isNoActiveTurn recognizes the provider's "nothing to interrupt" refusal.
//
// Matched on the message because that is all app-server gives: the code is the
// generic -32600 it uses for any invalid request, so the code alone cannot
// distinguish this from a malformed call.
func isNoActiveTurn(err error) bool {
	var rpcErr *rpcError
	if !errors.As(err, &rpcErr) {
		return false
	}
	return strings.Contains(strings.ToLower(rpcErr.Message), "no active turn")
}
