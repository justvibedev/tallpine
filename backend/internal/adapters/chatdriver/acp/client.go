package acp

import (
	"context"
	"encoding/json"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Tallpine deliberately advertises neither client-side filesystem nor terminal
// capabilities. Claude's ACP adapter uses Claude Code's native tools inside the
// worktree; routing those operations through Electron or the daemon would create
// a second execution/security model beside Tallpine's existing one.
func (c *conversation) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, errClientCapability
}

func (c *conversation) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, errClientCapability
}

func (c *conversation) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, errClientCapability
}

func (c *conversation) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, errClientCapability
}

func (c *conversation) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, errClientCapability
}

func (c *conversation) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, errClientCapability
}

func (c *conversation) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, errClientCapability
}

// UpdatePlan publishes a provider extension plan on the active Tallpine turn.
func (c *conversation) UpdatePlan(plan *domain.ConversationPlan) {
	c.mu.Lock()
	turnID := c.activeTurn
	c.mu.Unlock()
	c.emit(ports.ChatEvent{Kind: ports.ChatEventPlanUpdated, ProviderTurnID: turnID, Plan: plan})
}

// timeAfter is a variable so permission timeout behavior can be tested without
// sleeping for the production interval.
var timeAfter = time.After

func (c *conversation) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	// Keep replay normalization off the SDK's bounded notification queue.
	if c.captureReplayUpdate(params) {
		return nil
	}
	return c.processUpdate(params)
}

// handleNotification runs updates and host receipts in wire order, before the
// next frame is read. Permission requests retain the SDK's asynchronous
// dispatch, and responses cannot overtake the updates they follow.
func (c *conversation) handleNotification(method string, params json.RawMessage) bool {
	var err error
	switch method {
	case acpsdk.ClientMethodSessionUpdate:
		if c.handleAsyncTask(params) {
			return true
		}
		var update acpsdk.SessionNotification
		if err = json.Unmarshal(params, &update); err == nil {
			err = update.Validate()
		}
		if err == nil {
			err = c.SessionUpdate(context.Background(), update)
		}
	case persistenthost.ACPPromptResultMethod, persistenthost.ACPInteractionCommandMethod:
		_, err = c.HandleExtensionMethod(context.Background(), method, params)
	default:
		return false
	}
	if err != nil {
		c.log.Error("failed to handle ACP notification", "method", method, "err", err)
	}
	return true
}

func normalizePlan(entries []acpsdk.PlanEntry) *domain.ConversationPlan {
	plan := &domain.ConversationPlan{Steps: make([]domain.ConversationPlanStep, 0, len(entries))}
	for _, entry := range entries {
		status := domain.PlanStepPending
		switch entry.Status {
		case acpsdk.PlanEntryStatusInProgress:
			status = domain.PlanStepInProgress
		case acpsdk.PlanEntryStatusCompleted:
			status = domain.PlanStepCompleted
		}
		plan.Steps = append(plan.Steps, domain.ConversationPlanStep{Text: entry.Content, Status: status})
	}
	return plan
}

func pointerValue[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}
