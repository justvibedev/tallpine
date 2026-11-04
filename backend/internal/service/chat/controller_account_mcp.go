package chat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// uncorrelatedRecovery reports an account update that says only "recovered":
// no demand, auth mode, or plan. It carries no fact applyAccount can record.
func uncorrelatedRecovery(update ports.ChatAccount) bool {
	return update.ReauthRecovered && !update.ReauthRequired && update.AuthMode == "" && update.PlanLabel == ""
}

// applyAccount folds an account report into what Tallpine already knows and records the
// result.
//
// A merge rather than a replace because the provider reports the account in pieces:
// account/updated carries the auth mode and plan and says nothing about credentials,
// while a credential demand carries only the demand. Writing either straight through
// would blank the other, so a session whose tokens expired would lose the plan label
// and one that changed plan would look like its credentials were fine again.
//
// A demand for credentials also gets a timeline row. It is the one account fact the
// user has to act on, and a turn that failed because the provider wanted a login it
// could not get is otherwise indistinguishable from any other failed turn.
func (c *Controller) applyAccount(
	ctx context.Context,
	update ports.ChatAccount,
	now time.Time,
) error {
	// Uncorrelated account reports cannot establish recovery. Successful turn
	// completion is projected separately with its provider identity and fence.
	if uncorrelatedRecovery(update) {
		return nil
	}
	if err := c.recordAccount(ctx, update, now); err != nil {
		return err
	}
	if !update.ReauthRequired {
		return nil
	}
	detail, _ := json.Marshal(map[string]string{
		"event":  "auth.reauth_required",
		"reason": update.ReauthReason,
	})
	return c.store.UpsertActivity(ctx, c.conversation.ID, "",
		domain.ConversationActivity{
			ID:      c.newID(),
			Kind:    domain.ActivityKindSystem,
			Status:  domain.ActivityStatusFailed,
			Summary: "The provider needs you to sign in again",
			Detail:  detail,
			// Keyed on the reason so a provider retrying its demand updates one row
			// instead of filling the timeline with the same notice.
			ProviderItemID: "ao-reauth-" + cmp.Or(update.ReauthReason, "unknown"),
		}, now)
}

// recordAccount updates the latest account projection without creating a
// timeline row. A terminal provider failure already occupies the turn's outcome;
// recording recovery state here lets persistent UI explain what to do next
// without duplicating the same provider prose as another event.
func (c *Controller) recordAccount(
	ctx context.Context,
	update ports.ChatAccount,
	now time.Time,
) error {
	// Read the durable projection in the event transaction. Snapshot reconciliation
	// may have recovered it since this controller's last account report.
	previous, err := c.store.ConversationAccount(ctx, c.conversation.ID)
	if err != nil {
		return err
	}
	account := domain.ConversationAccount{AuthenticationState: "unknown"}
	if previous != nil {
		account = *previous
	}
	if update.AuthMode != "" && account.AuthMode != update.AuthMode {
		account.AuthChangedAt = &now
		if account.AuthMode != "" && account.AuthMode != update.AuthMode {
			account.AuthVerifiedAt = nil
			if account.ReauthRequiredAt == nil {
				account.AuthenticationState = "unknown"
			}
		}
		account.AuthMode = update.AuthMode
	}
	if update.PlanLabel != "" {
		account.PlanLabel = update.PlanLabel
	}
	if update.ReauthRequired {
		account.AuthenticationState = "required"
		account.ReauthRequiredAt = &now
		account.ReauthReason = update.ReauthReason
		account.LastAuthFailureAt = &now
		account.LastAuthFailureReason = update.ReauthReason
		account.AuthFailureID = c.newID()
		account.AuthVerifiedAt = nil
	}
	return c.store.RecordAccount(ctx, c.conversation.ID, account, now)
}

// applyThreadState folds a thread report into what Tallpine already knows.
//
// Tri-state on purpose. A status report says nothing about archiving and an archive
// report says nothing about status, so each report updates only what it actually
// spoke about — otherwise an ordinary idle report would silently un-archive a
// thread.
func (c *Controller) applyThreadState(
	ctx context.Context,
	update ports.ChatThreadState,
	now time.Time,
) error {
	c.mu.Lock()
	if update.Status != "" {
		c.threadState.Status = update.Status
		// The flags belong to the status report that carried them: an active thread
		// that is no longer waiting on an approval reports active with no flags, and
		// keeping the old list would leave it looking blocked forever.
		c.threadState.WaitingOn = update.WaitingOn
	}
	if update.Archived != nil {
		if *update.Archived {
			at := now
			c.threadState.ArchivedAt = &at
		} else {
			c.threadState.ArchivedAt = nil
		}
	}
	if update.Closed && c.threadState.ClosedAt == nil {
		at := now
		c.threadState.ClosedAt = &at
	}
	if update.Compaction != nil {
		next := &domain.ConversationCompaction{
			ID: update.Compaction.ID, Status: update.Compaction.Status, Trigger: update.Compaction.Trigger,
			PreTokens: update.Compaction.PreTokens, PostTokens: update.Compaction.PostTokens, At: now,
		}
		// A second terminal report enriches the first with its token counts; the
		// time it finished does not move.
		if prev := c.threadState.Compaction; prev != nil && prev.ID == next.ID && prev.Status == domain.CompactionCompleted &&
			next.Status == domain.CompactionCompleted {
			next.At = prev.At
			if next.Trigger == "" {
				next.Trigger = prev.Trigger
			}
			if next.PreTokens == 0 {
				next.PreTokens = prev.PreTokens
			}
		}
		c.threadState.Compaction = next
	}
	c.threadState.UpdatedAt = now
	state := c.threadState
	c.mu.Unlock()

	return c.store.RecordThreadState(ctx, c.conversation.ID, state)
}

// applyMCPServers merges one server's report into the list and records it.
//
// The provider announces servers one at a time and re-announces all of them on
// every turn, so this is a merge by name. First-seen order is preserved so the list
// a client renders does not reshuffle between polls.
func (c *Controller) applyMCPServers(ctx context.Context, updates []ports.ChatMCPServer) error {
	c.mcpPersistMu.Lock()
	defer c.mcpPersistMu.Unlock()

	c.mu.Lock()
	for _, update := range updates {
		if update.Name == "" {
			continue
		}
		if _, seen := c.mcpServers[update.Name]; !seen {
			c.mcpServerOrder = append(c.mcpServerOrder, update.Name)
		}
		c.mcpServers[update.Name] = domain.ConversationMCPServer{
			Name:          update.Name,
			Status:        update.Status,
			Error:         update.Error,
			FailureReason: update.FailureReason,
		}
		c.mcpServerRevision++
		c.mcpServerSeenRevision[update.Name] = c.mcpServerRevision
	}
	servers := c.mcpServersLocked()
	c.mu.Unlock()

	return c.store.RecordMCPServers(ctx, c.conversation.ID, servers)
}

func (c *Controller) mcpServersLocked() []domain.ConversationMCPServer {
	servers := make([]domain.ConversationMCPServer, 0, len(c.mcpServerOrder))
	for _, name := range c.mcpServerOrder {
		if server, ok := c.mcpServers[name]; ok {
			servers = append(servers, server)
		}
	}
	return servers
}

// ErrMCPReloadUnsupported reports a driver whose provider cannot restart its tool
// servers. Permanent rather than transient, so a client should stop offering it.
var ErrMCPReloadUnsupported = errors.New("chat driver cannot reload MCP servers")

// ReloadMCPServers restarts the provider's tool servers.
//
// It takes sendMu for the reason a compaction does: the reload tears down and
// re-establishes every tool the agent has, and doing that under a running turn would
// pull tools out from under work in flight. Refusing while busy makes the user stop
// the turn themselves rather than discovering afterwards from the timeline that
// their request failed for a reason they caused.
func (c *Controller) ReloadMCPServers(ctx context.Context) ([]domain.ConversationMCPServer, error) {
	reloader, ok := c.conv.(ports.ChatMCPReloader)
	if !ok {
		return nil, ErrMCPReloadUnsupported
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return nil, ErrControllerHandoff
	}

	if c.busy() {
		return nil, ErrTurnRunning
	}

	c.mu.Lock()
	reloadRevision := c.mcpServerRevision
	c.mu.Unlock()

	result, err := reloader.ReloadMCPServers(ctx)
	if err != nil {
		return nil, err
	}
	if !result.Authoritative {
		// Reload succeeded, but its inventory read did not. Preserve what is known:
		// an empty unavailable result cannot distinguish no configured servers from a
		// provider that could not enumerate them.
		c.mu.Lock()
		current := c.mcpServersLocked()
		c.mu.Unlock()
		return current, nil
	}

	// Startup notifications are individual deltas, while this inventory is a full
	// replacement. Keep only notifications observed after reload began, then overlay
	// positively initialized inventory entries as ready. Holding mcpPersistMu through
	// the write keeps a notification that raced the reload from persisting stale state
	// after this replacement.
	c.mcpPersistMu.Lock()
	defer c.mcpPersistMu.Unlock()
	c.mu.Lock()
	fresh := make(map[string]domain.ConversationMCPServer)
	freshOrder := make([]string, 0, len(c.mcpServerOrder))
	for _, name := range c.mcpServerOrder {
		if c.mcpServerSeenRevision[name] > reloadRevision {
			fresh[name] = c.mcpServers[name]
			freshOrder = append(freshOrder, name)
		}
	}
	for _, server := range result.Servers {
		if server.Name == "" {
			continue
		}
		current, seen := fresh[server.Name]
		if seen && current.Status != "ready" {
			// The status list is a snapshot, while this non-ready state came from
			// a startup notification observed after the reload began. Keep the
			// newer failure/transition instead of promoting it back to ready.
			continue
		}
		if !seen {
			freshOrder = append(freshOrder, server.Name)
		}
		fresh[server.Name] = domain.ConversationMCPServer{
			Name:          server.Name,
			Status:        server.Status,
			Error:         server.Error,
			FailureReason: server.FailureReason,
		}
	}
	c.mcpServers = fresh
	c.mcpServerOrder = freshOrder
	c.mcpServerSeenRevision = make(map[string]uint64, len(fresh))
	for name := range fresh {
		c.mcpServerSeenRevision[name] = c.mcpServerRevision
	}
	reconciled := c.mcpServersLocked()
	c.mu.Unlock()

	if err := c.store.RecordMCPServers(ctx, c.conversation.ID, reconciled); err != nil {
		return nil, err
	}
	return reconciled, nil
}
