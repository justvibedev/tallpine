package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// UpsertUsageBinding records or refreshes the association between a Tallpine
// session and a native root session/thread.
func (s *Store) UpsertUsageBinding(ctx context.Context, rec domain.UsageBindingRecord) (domain.UsageBindingRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	row, err := s.qw.UpsertUsageBinding(ctx, gen.UpsertUsageBindingParams{
		SessionID:      rec.SessionID,
		Harness:        rec.Harness,
		NativeRootID:   rec.NativeRootID,
		InitialModelID: strings.TrimSpace(rec.InitialModelID),
		ProviderHint:   strings.TrimSpace(rec.ProviderHint),
		State:          usageBindingStateOrDefault(rec.State),
		LastErrorCode:  rec.LastErrorCode,
		UpdatedAt:      timeOrNow(rec.UpdatedAt),
	})
	if err != nil {
		return domain.UsageBindingRecord{}, fmt.Errorf("upsert usage binding for session %s root %q: %w", rec.SessionID, rec.NativeRootID, err)
	}
	return usageBindingFromGen(row), nil
}

// GetUsageBinding returns one binding, or ok=false when absent.
func (s *Store) GetUsageBinding(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, nativeRootID string) (domain.UsageBindingRecord, bool, error) {
	row, err := s.qr.GetUsageBindingBySessionHarnessRoot(ctx, gen.GetUsageBindingBySessionHarnessRootParams{
		SessionID:    sessionID,
		Harness:      harness,
		NativeRootID: nativeRootID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UsageBindingRecord{}, false, nil
	}
	if err != nil {
		return domain.UsageBindingRecord{}, false, fmt.Errorf("get usage binding for session %s root %q: %w", sessionID, nativeRootID, err)
	}
	return usageBindingFromGen(row), true, nil
}

// ListUsageBindingsForSession returns every native usage binding for a session.
func (s *Store) ListUsageBindingsForSession(ctx context.Context, sessionID domain.SessionID) ([]domain.UsageBindingRecord, error) {
	rows, err := s.qr.ListUsageBindingsForSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list usage bindings for session %s: %w", sessionID, err)
	}
	out := make([]domain.UsageBindingRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageBindingFromGen(row))
	}
	return out, nil
}

// FinalizeUsageBindingsForSessionLaunch atomically moves a live session's
// bindings into finalization only while its durable runtime generation and
// session revision match the lifecycle observation.
func (s *Store) FinalizeUsageBindingsForSessionLaunch(
	ctx context.Context,
	sessionID domain.SessionID,
	expectedRuntimeLaunchID string,
	expectedSessionRevision int64,
	at time.Time,
) ([]domain.UsageBindingRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.FinalizeUsageBindingsForSessionLaunch(ctx, gen.FinalizeUsageBindingsForSessionLaunchParams{
		SessionID:               sessionID,
		ExpectedRuntimeLaunchID: expectedRuntimeLaunchID,
		ExpectedSessionRevision: expectedSessionRevision,
		FinalizedAt:             timeOrNow(at),
	})
	if err != nil {
		return nil, fmt.Errorf("finalize usage bindings for session %s launch %q: %w", sessionID, expectedRuntimeLaunchID, err)
	}
	out := make([]domain.UsageBindingRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageBindingFromGen(row))
	}
	return out, nil
}

// UpdateUsageBindingState updates only the binding lifecycle/error state.
func (s *Store) UpdateUsageBindingState(ctx context.Context, id int64, state domain.UsageBindingState, lastErrorCode string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateUsageBinding(ctx, gen.UpdateUsageBindingParams{
		ID:            id,
		State:         usageBindingStateOrDefault(state),
		LastErrorCode: lastErrorCode,
		UpdatedAt:     timeOrNow(at),
	})
	if err != nil {
		return false, fmt.Errorf("update usage binding %d: %w", id, err)
	}
	return n > 0, nil
}

// UpdateUsageBindingErrorCode refreshes binding diagnostics without changing
// lifecycle state chosen by a concurrent finalization or ingestion pass.
func (s *Store) UpdateUsageBindingErrorCode(ctx context.Context, id int64, lastErrorCode string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateUsageBinding(ctx, gen.UpdateUsageBindingParams{
		ID:            id,
		State:         "",
		LastErrorCode: lastErrorCode,
		UpdatedAt:     timeOrNow(at),
	})
	if err != nil {
		return false, fmt.Errorf("update usage binding %d error code: %w", id, err)
	}
	return n > 0, nil
}

// CompleteUsageBindingIfSettled atomically completes a finalizing binding only
// when every registered source generation is complete.
func (s *Store) CompleteUsageBindingIfSettled(ctx context.Context, bindingID int64, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.CompleteUsageBindingIfSettled(ctx, gen.CompleteUsageBindingIfSettledParams{
		UsageBindingID: bindingID,
		UpdatedAt:      timeOrNow(at),
	})
	if err != nil {
		return false, fmt.Errorf("complete settled usage binding %d: %w", bindingID, err)
	}
	return n > 0, nil
}

// InsertUsageSource records a physical JSONL source generation. Repeated calls
// for the same binding/path/generation return the existing row.
func (s *Store) InsertUsageSource(ctx context.Context, rec domain.UsageSourceRecord) (domain.UsageSourceRecord, error) {
	if err := validateParserStateObject(rec.ParserStateJSON); err != nil {
		return domain.UsageSourceRecord{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	row, err := s.qw.InsertUsageSource(ctx, usageSourceInsertParams(rec))
	if err != nil {
		return domain.UsageSourceRecord{}, fmt.Errorf("insert usage source for binding %d generation %d: %w", rec.BindingID, rec.Generation, err)
	}
	return usageSourceFromGen(row), nil
}

// ReplaceUsageSource atomically retires one source generation and inserts its
// replacement so startup or watcher reconciliation can never observe a gap.
func (s *Store) ReplaceUsageSource(
	ctx context.Context,
	oldSourceID int64,
	oldErrorCode string,
	rec domain.UsageSourceRecord,
	at time.Time,
) (domain.UsageSourceRecord, error) {
	if err := validateParserStateObject(rec.ParserStateJSON); err != nil {
		return domain.UsageSourceRecord{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var replaced domain.UsageSourceRecord
	err := s.inTx(ctx, "replace usage source", func(q *gen.Queries) error {
		n, err := q.UpdateUsageSourceLifecycle(ctx, gen.UpdateUsageSourceLifecycleParams{
			ID:            oldSourceID,
			State:         domain.UsageSourceComplete,
			FailureCount:  sql.NullInt64{},
			LastErrorCode: oldErrorCode,
			NextRetryAt:   sql.NullTime{},
			UpdatedAt:     timeOrNow(at),
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("usage source %d not found", oldSourceID)
		}
		row, err := q.InsertUsageSource(ctx, usageSourceInsertParams(rec))
		if err != nil {
			return err
		}
		replaced = usageSourceFromGen(row)
		return nil
	})
	if err != nil {
		return domain.UsageSourceRecord{}, fmt.Errorf("replace usage source %d: %w", oldSourceID, err)
	}
	return replaced, nil
}

// ListUsageSourcesForBinding returns all source generations for one native
// session binding.
func (s *Store) ListUsageSourcesForBinding(ctx context.Context, bindingID int64) ([]domain.UsageSourceRecord, error) {
	rows, err := s.qr.ListUsageSourcesForBinding(ctx, bindingID)
	if err != nil {
		return nil, fmt.Errorf("list usage sources for binding %d: %w", bindingID, err)
	}
	out := make([]domain.UsageSourceRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageSourceFromGen(row))
	}
	return out, nil
}

// ListWatchableUsageSources returns the latest source generation for every
// provider artifact attached to a resumable session. Watch registrations are
// rebuilt from this durable inventory after daemon and watcher restarts.
func (s *Store) ListWatchableUsageSources(ctx context.Context) ([]domain.UsageSourceRecord, error) {
	rows, err := s.qr.ListWatchableUsageSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list watchable usage sources: %w", err)
	}
	out := make([]domain.UsageSourceRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageSourceFromGen(row))
	}
	return out, nil
}

// HasPendingUsageDiscovery reports whether a live binding has a durable reason
// to retry source discovery. Healthy active bindings are excluded unless their
// provider has a dynamic child inventory, such as Kimi agents.
func (s *Store) HasPendingUsageDiscovery(ctx context.Context) (bool, error) {
	pending, err := s.qr.HasPendingUsageDiscovery(ctx)
	if err != nil {
		return false, fmt.Errorf("check pending usage discovery: %w", err)
	}
	return pending != 0, nil
}

// HasUsageSourceByPath reports whether any binding already registers a source
// at this exact artifact path, so one physical transcript cannot be bound to a
// second session and counted twice.
func (s *Store) HasUsageSourceByPath(ctx context.Context, artifactPath string) (bool, error) {
	exists, err := s.qr.ExistsUsageSourceByArtifactPath(ctx, artifactPath)
	if err != nil {
		return false, fmt.Errorf("check usage source by path: %w", err)
	}
	return exists != 0, nil
}

// ListLatestRetiredCodexReplacementClaimsByPath returns durable replacement
// claims for one exact provider artifact path on resumable bindings.
func (s *Store) ListLatestRetiredCodexReplacementClaimsByPath(
	ctx context.Context,
	artifactPath string,
) ([]domain.UsageSourceRecord, error) {
	rows, err := s.qr.ListLatestRetiredCodexReplacementClaimsByPath(ctx, artifactPath)
	if err != nil {
		return nil, fmt.Errorf("list retired Codex replacement claims: %w", err)
	}
	out := make([]domain.UsageSourceRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageSourceFromGen(row))
	}
	return out, nil
}

// ListUsageDiscoveryBindings returns live-session bindings that may need a
// main source, a relocated source, or newly-created subagent sources.
func (s *Store) ListUsageDiscoveryBindings(ctx context.Context, limit int64) ([]domain.UsageBindingRecord, error) {
	rows, err := s.qr.ListUsageDiscoveryBindings(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list usage discovery bindings: %w", err)
	}
	out := make([]domain.UsageBindingRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageBindingFromGen(row))
	}
	return out, nil
}

// ListUsageBindingsForCodexParent returns live bindings whose latest source
// matches one exact Codex parent native session. The collector validates the
// child edge from that source's parser state before registration.
func (s *Store) ListUsageBindingsForCodexParent(ctx context.Context, parentNativeSessionID string) ([]domain.UsageBindingRecord, error) {
	rows, err := s.qr.ListUsageBindingsForCodexParent(ctx, parentNativeSessionID)
	if err != nil {
		return nil, fmt.Errorf("list usage bindings for Codex parent: %w", err)
	}
	out := make([]domain.UsageBindingRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, usageBindingFromGen(row))
	}
	return out, nil
}

// GetUsageSourceForIngestion returns a source plus immutable binding/session
// facts needed by the ingestor.
func (s *Store) GetUsageSourceForIngestion(ctx context.Context, id int64) (domain.UsageSourceContext, bool, error) {
	row, err := s.qr.GetUsageSourceWithBindingAndSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.UsageSourceContext{}, false, nil
	}
	if err != nil {
		return domain.UsageSourceContext{}, false, fmt.Errorf("get usage source %d: %w", id, err)
	}
	return usageSourceContextFromGen(row), true, nil
}

// MarkUsageSourceState updates only the source lifecycle/error state.
func (s *Store) MarkUsageSourceState(ctx context.Context, id int64, state domain.UsageSourceState, lastErrorCode string, nextRetryAt *time.Time, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateUsageSourceLifecycle(ctx, gen.UpdateUsageSourceLifecycleParams{
		ID:            id,
		State:         usageSourceStateOrDefault(state),
		FailureCount:  sql.NullInt64{},
		LastErrorCode: lastErrorCode,
		NextRetryAt:   ptrTimeToNullTime(nextRetryAt),
		UpdatedAt:     timeOrNow(updatedAt),
	})
	if err != nil {
		return false, fmt.Errorf("mark usage source %d: %w", id, err)
	}
	return n > 0, nil
}

// ReactivateUsageSource makes a completed or failed source eligible for
// immediate resumed-session ingestion and clears transient retry state.
func (s *Store) ReactivateUsageSource(ctx context.Context, id int64, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateUsageSourceLifecycle(ctx, gen.UpdateUsageSourceLifecycleParams{
		ID:            id,
		State:         domain.UsageSourceActive,
		FailureCount:  sql.NullInt64{Int64: 0, Valid: true},
		LastErrorCode: "",
		NextRetryAt:   sql.NullTime{},
		UpdatedAt:     timeOrNow(updatedAt),
	})
	if err != nil {
		return false, fmt.Errorf("reactivate usage source %d: %w", id, err)
	}
	return n > 0, nil
}

// MarkUsageSourceFailure records a failed ingestion attempt and its bounded
// retry schedule.
func (s *Store) MarkUsageSourceFailure(ctx context.Context, id, failureCount int64, lastErrorCode string, nextRetryAt, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.UpdateUsageSourceLifecycle(ctx, gen.UpdateUsageSourceLifecycleParams{
		ID:            id,
		State:         domain.UsageSourceError,
		FailureCount:  sql.NullInt64{Int64: failureCount, Valid: true},
		LastErrorCode: lastErrorCode,
		NextRetryAt:   sql.NullTime{Time: nextRetryAt.UTC(), Valid: true},
		UpdatedAt:     timeOrNow(updatedAt),
	})
	if err != nil {
		return false, fmt.Errorf("mark usage source %d failure: %w", id, err)
	}
	return n > 0, nil
}
