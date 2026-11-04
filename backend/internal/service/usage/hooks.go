package usage

import (
	"context"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pricing"
)

// RecordHook registers transcript metadata and updates collection lifecycle for
// one native hook callback.
func (c *Collector) RecordHook(ctx context.Context, sessionID domain.SessionID, signal HookSignal) error {
	signal.LaunchID = boundedUsageMetadata(signal.LaunchID)
	finalizing := finalizingEvent(signal.Event)
	session, proceed, err := c.hookSession(ctx, sessionID, signal, finalizing)
	if err != nil || !proceed {
		return err
	}
	signal.Harness = session.Harness
	signal.ProviderHint = trustedClaudeProviderHint(session.Harness, signal.ProviderHint)
	signal.NativeSessionID = boundedUsageMetadata(signal.NativeSessionID)
	if signal.NativeSessionID == "" {
		signal.NativeSessionID = usageNativeSessionID(session)
	}
	if signal.NativeSessionID == "" {
		signal.NativeSessionID = nativeIDFromTranscript(signal.TranscriptPath)
	}

	mainPath := strings.TrimSpace(signal.TranscriptPath)
	existsForDiscovery := false
	if finalizing && signal.NativeSessionID != "" && session.Harness == domain.HarnessClaudeCode {
		_, existsForDiscovery, err = c.store.GetUsageBinding(ctx, sessionID, session.Harness, signal.NativeSessionID)
		if err != nil {
			return err
		}
	}
	if signal.NativeSessionID != "" && mainPath == "" &&
		(session.Harness == domain.HarnessCodex ||
			finalizing && !existsForDiscovery) {
		mainPath, err = c.discoverPath(ctx, session.Harness, signal.NativeSessionID)
		if err != nil {
			return err
		}
	}
	subagentPath := strings.TrimSpace(signal.SubagentTranscriptPath)
	mainArtifact, err := c.validateHookArtifact(ctx, session.Harness, mainPath)
	if err != nil {
		return err
	}
	subagentArtifact, err := c.validateHookArtifact(ctx, session.Harness, subagentPath)
	if err != nil {
		return err
	}
	if finalizing && !existsForDiscovery {
		recoveryPath := mainPath
		if recoveryPath == "" && session.Harness == domain.HarnessClaudeCode {
			recoveryPath = subagentPath
		}
		if recoveryPath == "" {
			return nil
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	session, proceed, err = c.hookSession(ctx, sessionID, signal, finalizing)
	if err != nil || !proceed {
		return err
	}
	now := c.now().UTC()
	if finalizing {
		if err := c.finalizeSession(ctx, sessionID, now); err != nil {
			return err
		}
	}
	if signal.NativeSessionID == "" {
		c.notifySourceInventory(!finalizing)
		return nil
	}
	existing, exists, err := c.store.GetUsageBinding(ctx, sessionID, session.Harness, signal.NativeSessionID)
	if err != nil {
		return err
	}
	sessionLive := !session.IsTerminated && session.Activity.State != domain.ActivityExited
	reactivating := sessionLive && !finalizing && (signal.Event == "session-start" ||
		exists && existing.State == domain.UsageBindingFinalizing)
	state := domain.UsageBindingActive
	if exists {
		state = existing.State
	}
	switch {
	case finalizing:
		state = domain.UsageBindingFinalizing
	case reactivating:
		state = domain.UsageBindingActive
	case exists && (state == domain.UsageBindingComplete || state == domain.UsageBindingPartial) &&
		signal.Event == "subagent-stop":
		state = domain.UsageBindingFinalizing
	}
	inventoryChanged := !exists || state != existing.State
	needsReconcile := false
	lastErrorCode := ""
	if session.Harness == domain.HarnessCodex && strings.TrimSpace(signal.TranscriptPath) == "" {
		lastErrorCode = domain.UsageErrorSourceDiscoveryPending
	}
	// A binding that existed without a billable route, and now has one, owns
	// history whose inferred or unavailable price can be corrected immediately.
	routeResolved := exists && signal.ProviderHint != "" &&
		(existing.ProviderHint == "" || existing.ProviderHint == pricing.UnidentifiedBillingRoute) &&
		existing.ProviderHint != signal.ProviderHint
	binding, err := c.store.UpsertUsageBinding(ctx, domain.UsageBindingRecord{
		SessionID:      sessionID,
		Harness:        session.Harness,
		NativeRootID:   signal.NativeSessionID,
		InitialModelID: boundedUsageMetadata(signal.ModelID),
		ProviderHint:   signal.ProviderHint,
		State:          state,
		LastErrorCode:  lastErrorCode,
		UpdatedAt:      now,
	})
	if err != nil {
		return err
	}
	if routeResolved {
		if notify := c.routeResolvedHandler(); notify != nil {
			notify()
		}
	}

	if mainArtifact != nil {
		kind, ok := sourceKindForHarness(session.Harness)
		if !ok {
			return fmt.Errorf("usage: unsupported harness %q", session.Harness)
		}
		changed, err := c.registerHookSource(
			ctx,
			binding,
			kind,
			signal.NativeSessionID,
			"",
			*mainArtifact,
			now,
			signal.Event == "session-start" || finalizing,
		)
		if err != nil {
			c.notifySourceInventory(true)
			return err
		}
		inventoryChanged = inventoryChanged || changed
		sourceErrorCode := ""
		if c.codexDiscoveryStillPending(ctx, signal.Event, signal.TranscriptPath, mainPath) {
			sourceErrorCode = domain.UsageErrorSourceDiscoveryPending
		}
		if _, err := c.store.UpdateUsageBindingErrorCode(ctx, binding.ID, sourceErrorCode, now); err != nil {
			return err
		}
	} else {
		needsReconcile = true
	}
	if subagentArtifact != nil && session.Harness == domain.HarnessClaudeCode {
		changed, err := c.registerHookSource(
			ctx,
			binding,
			domain.UsageSourceClaudeSubagent,
			signal.NativeSessionID,
			boundedUsageMetadata(signal.SubagentID),
			*subagentArtifact,
			now,
			finalizing || signal.Event == "subagent-stop",
		)
		if err != nil {
			c.notifySourceInventory(true)
			return err
		}
		inventoryChanged = inventoryChanged || changed
	}
	if reactivating {
		changed, err := c.reactivateBinding(ctx, binding, now)
		if err != nil {
			return err
		}
		inventoryChanged = inventoryChanged || changed
		if session.Harness == domain.HarnessCodex {
			needsReconcile = true
		}
		if c.codexDiscoveryStillPending(ctx, signal.Event, signal.TranscriptPath, mainPath) {
			if _, err := c.store.UpdateUsageBindingState(
				ctx,
				binding.ID,
				domain.UsageBindingActive,
				domain.UsageErrorSourceDiscoveryPending,
				now,
			); err != nil {
				return err
			}
		}
	} else if state == domain.UsageBindingFinalizing {
		if err := c.settleFinalizingBinding(ctx, binding.ID, now); err != nil {
			return err
		}
	}
	switch {
	case needsReconcile:
		c.notifySourceInventory(true)
	case inventoryChanged:
		c.notifySourceInventory(false)
	}
	return nil
}

func trustedClaudeProviderHint(harness domain.AgentHarness, raw string) string {
	if harness != domain.HarnessClaudeCode {
		return ""
	}
	return pricing.TrustedClaudeRoute(boundedUsageMetadata(raw))
}

func finalizingEvent(event string) bool {
	return event == "session-end" || event == "process-exited"
}

func (c *Collector) hookSession(
	ctx context.Context,
	sessionID domain.SessionID,
	signal HookSignal,
	finalizing bool,
) (domain.SessionRecord, bool, error) {
	session, ok, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return domain.SessionRecord{}, false, err
	}
	if !ok {
		return domain.SessionRecord{}, false, fmt.Errorf("%w: %s", ErrUsageSessionNotFound, sessionID)
	}
	if !SupportedHarness(session.Harness) {
		return session, false, nil
	}
	if signal.LaunchID != "" && session.Metadata.RuntimeLaunchID != "" &&
		signal.LaunchID != session.Metadata.RuntimeLaunchID {
		c.logger.Warn("usage hook dropped: launch id mismatch",
			"session", sessionID, "event", signal.Event, "hook_launch_id", signal.LaunchID)
		return session, false, nil
	}
	if signal.Harness != "" && signal.Harness != session.Harness {
		return domain.SessionRecord{}, false, fmt.Errorf(
			"usage hook harness %s does not match session harness %s",
			signal.Harness,
			session.Harness,
		)
	}
	sessionLive := !session.IsTerminated && session.Activity.State != domain.ActivityExited
	if session.IsTerminated || (!finalizing && !sessionLive) {
		c.logger.Warn("usage hook dropped: session not live",
			"session", sessionID, "event", signal.Event,
			"terminated", session.IsTerminated, "activity_state", session.Activity.State)
		return session, false, nil
	}
	return session, true, nil
}

func (c *Collector) validateHookArtifact(
	ctx context.Context,
	harness domain.AgentHarness,
	path string,
) (*validatedSourceArtifact, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	resolved, identity, size, err := c.validateSourcePath(ctx, harness, path)
	if err != nil {
		return nil, err
	}
	return &validatedSourceArtifact{path: resolved, identity: identity, size: size}, nil
}
