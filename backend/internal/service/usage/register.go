package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (c *Collector) registerSource(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	kind domain.UsageSourceKind,
	nativeSessionID string,
	subagentID string,
	path string,
	now time.Time,
	reactivateExisting bool,
) (bool, error) {
	expectedParentID := ""
	if kind == domain.UsageSourceCodexRollout && subagentID != "" {
		expectedParentID = binding.NativeRootID
	}
	return c.registerSourceWithExpectedParent(
		ctx,
		binding,
		kind,
		nativeSessionID,
		subagentID,
		path,
		now,
		reactivateExisting,
		expectedParentID,
	)
}

func (c *Collector) registerHookSource(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	kind domain.UsageSourceKind,
	nativeSessionID string,
	subagentID string,
	artifact validatedSourceArtifact,
	now time.Time,
	reactivateExisting bool,
) (bool, error) {
	expectedParentID := ""
	if kind == domain.UsageSourceCodexRollout && subagentID != "" {
		expectedParentID = binding.NativeRootID
	}
	if err := validateSourceAttribution(
		binding,
		kind,
		nativeSessionID,
		subagentID,
		artifact.path,
		expectedParentID,
	); err != nil {
		return false, err
	}
	sources, err := c.store.ListUsageSourcesForBinding(ctx, binding.ID)
	if err != nil {
		return false, err
	}
	return c.registerValidatedSource(
		ctx,
		binding,
		kind,
		nativeSessionID,
		subagentID,
		expectedParentID,
		artifact.path,
		artifact.identity,
		artifact.size,
		now,
		reactivateExisting,
		newBindingSourceInventory(sources),
	)
}

func (c *Collector) registerSourceWithExpectedParent(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	kind domain.UsageSourceKind,
	nativeSessionID string,
	subagentID string,
	path string,
	now time.Time,
	reactivateExisting bool,
	expectedParentID string,
) (bool, error) {
	resolved, identity, size, err := c.validateSourcePath(ctx, binding.Harness, path)
	if err != nil {
		return false, err
	}
	if err := validateSourceAttribution(
		binding,
		kind,
		nativeSessionID,
		subagentID,
		resolved,
		expectedParentID,
	); err != nil {
		return false, err
	}
	sources, err := c.store.ListUsageSourcesForBinding(ctx, binding.ID)
	if err != nil {
		return false, err
	}
	return c.registerValidatedSource(
		ctx,
		binding,
		kind,
		nativeSessionID,
		subagentID,
		expectedParentID,
		resolved,
		identity,
		size,
		now,
		reactivateExisting,
		newBindingSourceInventory(sources),
	)
}

func (c *Collector) registerSourceWithInventory(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	kind domain.UsageSourceKind,
	nativeSessionID string,
	subagentID string,
	path string,
	now time.Time,
	reactivateExisting bool,
	inventory *bindingSourceInventory,
	expectedParentID string,
) (bool, error) {
	resolved, identity, size, err := c.validateSourcePath(ctx, binding.Harness, path)
	if err != nil {
		return false, err
	}
	if err := validateSourceAttribution(
		binding,
		kind,
		nativeSessionID,
		subagentID,
		resolved,
		expectedParentID,
	); err != nil {
		return false, err
	}
	return c.registerValidatedSource(
		ctx,
		binding,
		kind,
		nativeSessionID,
		subagentID,
		expectedParentID,
		resolved,
		identity,
		size,
		now,
		reactivateExisting,
		inventory,
	)
}

func (c *Collector) registerValidatedSource(
	ctx context.Context,
	binding domain.UsageBindingRecord,
	kind domain.UsageSourceKind,
	nativeSessionID string,
	subagentID string,
	directParentID string,
	resolved string,
	identity string,
	size int64,
	now time.Time,
	reactivateExisting bool,
	inventory *bindingSourceInventory,
) (bool, error) {
	if c.codexSourceBudgetExceeded(inventory, kind, nativeSessionID, subagentID) {
		state := binding.State
		if state == domain.UsageBindingDiscovering || state == domain.UsageBindingActive {
			state = domain.UsageBindingPartial
		}
		_, err := c.store.UpdateUsageBindingState(
			ctx,
			binding.ID,
			state,
			domain.UsageErrorCodexSourceBudgetExceeded,
			now,
		)
		return false, err
	}
	latest := inventory.latestByPath[resolved]
	latestNative := inventory.latestCodexByNative[nativeSessionID]
	identityMatch := inventory.identityMatch(kind, nativeSessionID, identity, size)
	generation := inventory.maxGeneration
	if latest != nil && latest.FileIdentity == identity && size >= latest.ByteOffset &&
		latest.LastErrorCode != domain.UsageErrorArtifactReplaced {
		if reactivateExisting || latest.State == domain.UsageSourceError {
			changed, err := c.store.ReactivateUsageSource(ctx, latest.ID, now)
			if err == nil && changed {
				inventory.reactivate(latest.ID, now)
			}
			return changed, err
		}
		return false, nil
	}
	if len(inventory.sources) > 0 {
		generation++
	}
	var replaced *domain.UsageSourceRecord
	switch {
	case latest != nil:
		replaced = latest
	case kind == domain.UsageSourceCodexRollout && latestNative != nil:
		replaced = latestNative
	case identityMatch != nil:
		replaced = identityMatch
	}
	record := domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            kind,
		NativeSessionID: nativeSessionID,
		SubagentID:      subagentID,
		ArtifactPath:    resolved,
		FileIdentity:    identity,
		Generation:      generation,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	}
	if kind == domain.UsageSourceCodexRollout {
		state, err := initialCodexParserState(nativeSessionID, directParentID)
		if err != nil {
			return false, err
		}
		record.ParserStateJSON = state
	}
	if latest == nil && identityMatch != nil {
		resumeState := true
		if kind == domain.UsageSourceCodexRollout {
			storedNativeID, storedParentID, ok := codexParserAttribution(identityMatch.ParserStateJSON)
			resumeState = ok && storedNativeID == nativeSessionID && storedParentID == directParentID
		}
		if resumeState {
			record.ByteOffset = identityMatch.ByteOffset
			record.ParserStateJSON = identityMatch.ParserStateJSON
		}
	}
	var inserted domain.UsageSourceRecord
	var err error
	if replaced != nil {
		inserted, err = c.store.ReplaceUsageSource(ctx, replaced.ID, domain.UsageErrorArtifactReplaced, record, now)
	} else {
		inserted, err = c.store.InsertUsageSource(ctx, record)
	}
	if err != nil {
		return false, err
	}
	if replaced != nil {
		inventory.markState(replaced.ID, domain.UsageSourceComplete, domain.UsageErrorArtifactReplaced, now)
	}
	inventory.add(inserted)
	return true, nil
}

func initialCodexParserState(nativeSessionID, directParentID string) (string, error) {
	state := struct {
		Version    int                    `json:"version"`
		SourceKind domain.UsageSourceKind `json:"source_kind"`
		Integrity  struct{}               `json:"integrity"`
		Codex      struct {
			Baseline            struct{} `json:"baseline"`
			NativeSessionID     string   `json:"native_session_id"`
			DirectParentID      string   `json:"direct_parent_id,omitempty"`
			PendingSpawnCallIDs []string `json:"pending_spawn_call_ids"`
			DiscoveredChildIDs  []string `json:"discovered_child_ids"`
		} `json:"codex"`
	}{
		Version:    1,
		SourceKind: domain.UsageSourceCodexRollout,
	}
	state.Codex.NativeSessionID = nativeSessionID
	state.Codex.DirectParentID = directParentID
	state.Codex.PendingSpawnCallIDs = []string{}
	state.Codex.DiscoveredChildIDs = []string{}
	encoded, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode Codex parser state: %w", err)
	}
	return string(encoded), nil
}

func (c *Collector) codexSourceBudgetExceeded(
	inventory *bindingSourceInventory,
	kind domain.UsageSourceKind,
	nativeSessionID string,
	subagentID string,
) bool {
	if kind != domain.UsageSourceCodexRollout || nativeSessionID == "" || subagentID == "" {
		return false
	}
	if _, exists := inventory.codexLogicalIDs[nativeSessionID]; exists {
		return false
	}
	return len(inventory.codexLogicalIDs) >= c.codexLogicalSourceLimit
}
