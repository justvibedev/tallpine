package usage

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// BackfillActive discovers transcript files only for live/resumable Tallpine
// sessions. It deliberately does not import terminated session history.
func (c *Collector) BackfillActive(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	sessions, err := c.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, session := range sessions {
		if session.IsTerminated || !SupportedHarness(session.Harness) {
			continue
		}
		nativeID := usageNativeSessionID(session)
		if nativeID == "" || !nativeUsageIDPattern.MatchString(nativeID) {
			continue
		}
		if err := c.backfillSession(ctx, session, nativeID); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// usageNativeSessionID returns the provider transcript identity for the
// session's committed interface. Chat controllers persist that identity as the
// provider conversation id because they have no terminal hook to populate the
// agent session id. TUI controllers populate the agent session id through their
// native hook pipeline. Prefer the Chat field only when it is present so older
// migrated records can still fall back to their hook-derived identity.
func usageNativeSessionID(session domain.SessionRecord) string {
	nativeID := session.Metadata.AgentSessionID
	if domain.NormalizeSessionMode(session.Mode) == domain.SessionModeChat &&
		strings.TrimSpace(session.Metadata.ProviderConversationID) != "" {
		nativeID = session.Metadata.ProviderConversationID
	}
	return boundedUsageMetadata(nativeID)
}

func (c *Collector) backfillSession(ctx context.Context, session domain.SessionRecord, nativeID string) error {
	now := c.now().UTC()
	existing, exists, err := c.store.GetUsageBinding(ctx, session.ID, session.Harness, nativeID)
	if err != nil {
		return err
	}
	path, err := c.discoverPath(ctx, session.Harness, nativeID)
	if err != nil {
		return err
	}
	if exists && session.Activity.State == domain.ActivityExited &&
		(existing.State == domain.UsageBindingComplete || existing.State == domain.UsageBindingPartial) {
		return nil
	}
	if exists && session.Activity.State != domain.ActivityExited {
		budgetLimited := existing.LastErrorCode == domain.UsageErrorCodexSourceBudgetExceeded
		if _, err := c.reactivateBinding(ctx, existing, now); err != nil {
			return err
		}
		existing.State = domain.UsageBindingActive
		existing.LastErrorCode = ""
		if budgetLimited {
			existing.State = domain.UsageBindingPartial
			existing.LastErrorCode = domain.UsageErrorCodexSourceBudgetExceeded
		}
	}

	state := existing.State
	if !exists {
		state = domain.UsageBindingActive
		switch {
		case session.Activity.State == domain.ActivityExited:
			state = domain.UsageBindingFinalizing
		case path == "":
			state = domain.UsageBindingDiscovering
		}
	} else if session.Activity.State == domain.ActivityExited &&
		(state == domain.UsageBindingDiscovering || state == domain.UsageBindingActive) {
		state = domain.UsageBindingFinalizing
	} else if state == domain.UsageBindingDiscovering && path != "" {
		state = domain.UsageBindingActive
	}
	lastErrorCode := ""
	if path == "" {
		lastErrorCode = domain.UsageErrorSourceDiscoveryPending
	}
	binding, err := c.store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
		SessionID:      session.ID,
		Harness:        session.Harness,
		NativeRootID:   nativeID,
		InitialModelID: existing.InitialModelID,
		ProviderHint:   existing.ProviderHint,
		State:          state,
		LastErrorCode:  lastErrorCode,
		UpdatedAt:      now,
	})
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	kind, ok := sourceKindForHarness(session.Harness)
	if !ok {
		return fmt.Errorf("usage: unsupported harness %q", session.Harness)
	}
	if _, err := c.registerSource(ctx, binding, kind, nativeID, "", path, now, false); err != nil {
		return err
	}
	switch session.Harness {
	case domain.HarnessClaudeCode:
		if err := c.registerDiscoveredClaudeSubagents(ctx, binding, path, now, false); err != nil {
			return err
		}
	case domain.HarnessCodex:
		if err := c.registerDiscoveredCodexChildren(ctx, binding, now); err != nil {
			return err
		}
	}
	if state == domain.UsageBindingFinalizing {
		return c.settleFinalizingBinding(ctx, binding.ID, now)
	}
	return nil
}
