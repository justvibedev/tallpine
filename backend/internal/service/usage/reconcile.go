package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ReconcileSources discovers provider artifacts that hooks could not name,
// appeared after their hook, or moved between provider-owned directories.
// A negative limit reconciles every eligible binding; zero uses the bounded
// default.
func (c *Collector) ReconcileSources(ctx context.Context, limit int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if limit == 0 {
		limit = defaultDiscoveryLimit
	}
	bindings, err := c.store.ListUsageDiscoveryBindings(ctx, limit)
	if err != nil {
		return err
	}
	// One refresh serves the whole pass; per-binding lookups must not rescan.
	if err := c.claudeProjects.refresh(ctx); err != nil {
		return err
	}
	now := c.now().UTC()
	var errs []error
	for _, binding := range bindings {
		if err := c.reconcileBinding(ctx, binding, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ReconcilePath uses Codex rollout metadata to validate replacements and reach
// newly-created child bindings independently of the bounded discovery queue.
func (c *Collector) ReconcilePath(ctx context.Context, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	resolved, _, _, err := c.validateSourcePath(ctx, domain.HarnessCodex, path)
	if err != nil {
		return nil
	}
	meta, ok := readCodexSessionMeta(resolved)
	if !ok || len(meta.NativeSessionID) > maxUsageMetadataBytes ||
		!nativeUsageIDPattern.MatchString(meta.NativeSessionID) {
		return nil
	}
	claims, err := c.store.ListLatestRetiredCodexReplacementClaimsByPath(ctx, resolved)
	if err != nil {
		return err
	}
	now := c.now().UTC()
	if len(claims) > 0 {
		return c.reconcileRetiredCodexClaims(ctx, resolved, meta, claims, now)
	}
	if meta.ParentThreadID == "" || !validCanonicalUUID(meta.NativeSessionID) ||
		len(meta.ParentThreadID) > maxUsageMetadataBytes ||
		!nativeUsageIDPattern.MatchString(meta.ParentThreadID) {
		return nil
	}
	bindings, err := c.store.ListUsageBindingsForCodexParent(ctx, meta.ParentThreadID)
	if err != nil {
		return err
	}
	var errs []error
	for _, binding := range bindings {
		sources, listErr := c.store.ListUsageSourcesForBinding(ctx, binding.ID)
		if listErr != nil {
			errs = append(errs, listErr)
			continue
		}
		if !latestCodexSourceDiscovers(sources, meta.ParentThreadID, meta.NativeSessionID) {
			continue
		}
		if _, registerErr := c.registerSourceWithExpectedParent(
			ctx,
			binding,
			domain.UsageSourceCodexRollout,
			meta.NativeSessionID,
			meta.NativeSessionID,
			resolved,
			now,
			false,
			meta.ParentThreadID,
		); registerErr != nil {
			errs = append(errs, registerErr)
		}
	}
	return errors.Join(errs...)
}

func (c *Collector) reconcileRetiredCodexClaims(
	ctx context.Context,
	path string,
	meta codexSessionMeta,
	claims []domain.UsageSourceRecord,
	now time.Time,
) error {
	var errs []error
	for _, claim := range claims {
		if claim.SubagentID == "" {
			if meta.NativeSessionID != claim.NativeSessionID || meta.ParentThreadID != "" {
				continue
			}
			bindings, err := c.store.ListUsageBindingsForCodexParent(ctx, claim.NativeSessionID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, binding := range bindings {
				if binding.ID != claim.BindingID || binding.NativeRootID != claim.NativeSessionID {
					continue
				}
				if _, err := c.registerSourceWithExpectedParent(
					ctx,
					binding,
					domain.UsageSourceCodexRollout,
					claim.NativeSessionID,
					"",
					path,
					now,
					false,
					"",
				); err != nil {
					errs = append(errs, err)
				}
			}
			continue
		}

		storedNativeID, directParentID, ok := codexParserAttribution(claim.ParserStateJSON)
		if !ok || storedNativeID != claim.NativeSessionID || meta.NativeSessionID != claim.NativeSessionID ||
			claim.SubagentID != claim.NativeSessionID ||
			meta.ParentThreadID != directParentID ||
			!validCanonicalUUID(meta.NativeSessionID) ||
			len(directParentID) > maxUsageMetadataBytes ||
			!nativeUsageIDPattern.MatchString(directParentID) {
			continue
		}
		bindings, err := c.store.ListUsageBindingsForCodexParent(ctx, directParentID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, binding := range bindings {
			if binding.ID != claim.BindingID {
				continue
			}
			sources, err := c.store.ListUsageSourcesForBinding(ctx, binding.ID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !latestCodexSourceDiscovers(sources, directParentID, meta.NativeSessionID) {
				continue
			}
			if _, err := c.registerSourceWithExpectedParent(
				ctx,
				binding,
				domain.UsageSourceCodexRollout,
				claim.NativeSessionID,
				claim.SubagentID,
				path,
				now,
				false,
				directParentID,
			); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func codexParserAttribution(raw string) (string, string, bool) {
	var state struct {
		Version    int                    `json:"version"`
		SourceKind domain.UsageSourceKind `json:"source_kind"`
		Codex      *struct {
			NativeSessionID string `json:"native_session_id"`
			DirectParentID  string `json:"direct_parent_id"`
		} `json:"codex"`
	}
	if json.Unmarshal([]byte(raw), &state) != nil || state.Version != 1 ||
		state.SourceKind != domain.UsageSourceCodexRollout || state.Codex == nil ||
		!nativeUsageIDPattern.MatchString(state.Codex.NativeSessionID) ||
		(state.Codex.DirectParentID != "" && !validCanonicalUUID(state.Codex.DirectParentID)) {
		return "", "", false
	}
	return state.Codex.NativeSessionID, state.Codex.DirectParentID, true
}

func (c *Collector) reconcileBinding(ctx context.Context, binding domain.UsageBindingRecord, now time.Time) error {
	session, ok, err := c.store.GetSession(ctx, binding.SessionID)
	if err != nil || !ok {
		return err
	}
	if session.IsTerminated && binding.State != domain.UsageBindingFinalizing {
		return nil
	}

	targetState := binding.State
	if session.Activity.State == domain.ActivityExited &&
		(binding.State == domain.UsageBindingDiscovering ||
			binding.State == domain.UsageBindingActive ||
			(binding.State == domain.UsageBindingPartial &&
				binding.LastErrorCode == domain.UsageErrorCodexSourceBudgetExceeded)) {
		targetState = domain.UsageBindingFinalizing
	}

	path, err := c.discoverIndexedPath(ctx, binding.Harness, binding.NativeRootID)
	if err != nil {
		return err
	}
	if path == "" {
		if binding.Harness == domain.HarnessCodex {
			if childErr := c.registerDiscoveredCodexChildren(ctx, binding, now); childErr != nil {
				return childErr
			}
		}
		targetState, lastErrorCode, stateErr := c.preserveCodexBudgetState(
			ctx,
			binding,
			targetState,
			domain.UsageErrorSourceDiscoveryPending,
		)
		if stateErr != nil {
			return stateErr
		}
		_, err = c.store.UpdateUsageBindingState(ctx, binding.ID, targetState, lastErrorCode, now)
		return err
	}
	if targetState == domain.UsageBindingDiscovering {
		targetState = domain.UsageBindingActive
	}

	kind, ok := sourceKindForHarness(binding.Harness)
	if !ok {
		return fmt.Errorf("usage: unsupported harness %q", binding.Harness)
	}
	if _, err := c.registerSource(ctx, binding, kind, binding.NativeRootID, "", path, now, false); err != nil {
		return err
	}
	switch binding.Harness {
	case domain.HarnessClaudeCode:
		if err := c.registerDiscoveredClaudeSubagents(ctx, binding, path, now, false); err != nil {
			return err
		}
		if err := c.discoverClaudeContinuations(ctx, binding, session, path, now); err != nil {
			return err
		}
	case domain.HarnessCodex:
		if err := c.registerDiscoveredCodexChildren(ctx, binding, now); err != nil {
			return err
		}
	}
	lastErrorCode := ""
	if binding.Harness == domain.HarnessCodex &&
		binding.LastErrorCode == domain.UsageErrorSourceDiscoveryPending &&
		targetState == domain.UsageBindingActive &&
		!pathWithinRoot(ctx, path, c.roots.CodexSessions) {
		lastErrorCode = domain.UsageErrorSourceDiscoveryPending
	}
	targetState, lastErrorCode, err = c.preserveCodexBudgetState(
		ctx,
		binding,
		targetState,
		lastErrorCode,
	)
	if err != nil {
		return err
	}
	if _, err := c.store.UpdateUsageBindingState(ctx, binding.ID, targetState, lastErrorCode, now); err != nil {
		return err
	}
	if targetState == domain.UsageBindingFinalizing {
		return c.settleFinalizingBinding(ctx, binding.ID, now)
	}
	return nil
}

func (c *Collector) preserveCodexBudgetState(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	state domain.UsageBindingState,
	errorCode string,
) (domain.UsageBindingState, string, error) {
	if binding.Harness != domain.HarnessCodex {
		return state, errorCode, nil
	}
	current, ok, err := c.store.GetUsageBinding(
		ctx,
		binding.SessionID,
		binding.Harness,
		binding.NativeRootID,
	)
	if err != nil || !ok {
		return state, errorCode, err
	}
	if current.LastErrorCode == domain.UsageErrorCodexSourceBudgetExceeded {
		if state == domain.UsageBindingFinalizing {
			return state, current.LastErrorCode, nil
		}
		return current.State, current.LastErrorCode, nil
	}
	return state, errorCode, nil
}
