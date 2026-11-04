package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Daemon-owned user preferences.
//
// The row is seeded by migration, so a read is a plain SELECT and no caller has
// to handle "settings do not exist yet".

// AppSettings is the durable preference set. Field-compatible with
// service/settings.Snapshot, which the daemon wiring adapts.
type AppSettings struct {
	HarnessDefaults map[string]domain.HarnessDefault
	// DefaultSessionMode is the interface a new session gets when the spawn does
	// not name one. Never applied to an existing session: only an explicit
	// interface transition changes a live session's committed mode, so
	// changing this only affects sessions created afterwards.
	DefaultSessionMode domain.SessionMode
	// VersionRefresh moves idle open sessions to a harness release installed
	// while they ran.
	VersionRefresh bool
	// DynamicOrchestratorContext lets an orchestrator raise its compaction
	// window while it researches (B413).
	DynamicOrchestratorContext bool
	// StorageCompaction coalesces repeated activity writes and lets the archive
	// janitor compact and expire raw provider events.
	StorageCompaction bool
	// ProviderEventRetentionDays is how long a terminated session's provider
	// events are kept.
	ProviderEventRetentionDays int
	// ChatHibernation is the idle Chat hibernation preference.
	ChatHibernation domain.ChatHibernationPolicy
	// WorkflowDefaults are the app-wide values projects inherit (B525).
	WorkflowDefaults domain.WorkflowDefaults
	UpdatedAt        time.Time
}

// GetAppSettings reads the preference row.
func (s *Store) GetAppSettings(ctx context.Context) (AppSettings, error) {
	row, err := s.qr.GetAppSettings(ctx)
	if err != nil {
		return AppSettings{}, fmt.Errorf("read app settings: %w", err)
	}
	defaults := make(map[string]domain.HarnessDefault)
	if err := json.Unmarshal([]byte(row.HarnessDefaults), &defaults); err != nil {
		return AppSettings{}, fmt.Errorf("read harness defaults: %w", err)
	}
	return AppSettings{
		WorkflowDefaults: decodeWorkflowDefaults(row.WorkflowDefaults),
		HarnessDefaults:  defaults,
		// Normalized on read: a value written by a build that knows a mode this
		// one does not must still resolve to something dispatchable.
		DefaultSessionMode:         domain.NormalizeSessionMode(row.DefaultSessionMode),
		VersionRefresh:             row.VersionRefresh,
		DynamicOrchestratorContext: row.DynamicOrchestratorContext,
		StorageCompaction:          row.StorageCompaction,
		ProviderEventRetentionDays: int(row.ProviderEventRetentionDays),
		ChatHibernation: domain.ChatHibernationPolicy{
			Enabled:          row.ChatHibernation,
			WorkerIdle:       time.Duration(row.ChatHibernationWorkerIdleSeconds) * time.Second,
			OrchestratorIdle: time.Duration(row.ChatHibernationOrchestratorIdleSeconds) * time.Second,
		},
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// SetDefaultSessionMode persists the default interface for new sessions.
func (s *Store) SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode, now time.Time) error {
	if !mode.Valid() {
		return fmt.Errorf("invalid session mode %q", mode)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetDefaultSessionMode(ctx, gen.SetDefaultSessionModeParams{
		DefaultSessionMode: mode,
		UpdatedAt:          now,
	}); err != nil {
		return fmt.Errorf("set session mode: %w", err)
	}
	return nil
}

// SetDynamicOrchestratorContext persists the global orchestrator research mode toggle.
func (s *Store) SetDynamicOrchestratorContext(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetDynamicOrchestratorContext(ctx, gen.SetDynamicOrchestratorContextParams{
		DynamicOrchestratorContext: enabled,
		UpdatedAt:                  now,
	}); err != nil {
		return fmt.Errorf("set dynamic orchestrator context: %w", err)
	}
	return nil
}

// SetVersionRefresh persists whether open sessions move to new harness releases.
func (s *Store) SetVersionRefresh(ctx context.Context, enabled bool, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetVersionRefresh(ctx, gen.SetVersionRefreshParams{
		VersionRefresh: enabled,
		UpdatedAt:      now,
	}); err != nil {
		return fmt.Errorf("set version refresh: %w", err)
	}
	return nil
}

// SetStorageCompaction persists the storage-compaction toggle and the provider
// event retention window, and applies the toggle to this store's write path.
func (s *Store) SetStorageCompaction(ctx context.Context, enabled bool, retentionDays int, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetStorageCompaction(ctx, gen.SetStorageCompactionParams{
		StorageCompaction:          enabled,
		ProviderEventRetentionDays: int64(retentionDays),
		UpdatedAt:                  now,
	}); err != nil {
		return fmt.Errorf("set storage compaction: %w", err)
	}
	s.coalesceActivityWrites.Store(enabled)
	return nil
}

// SetChatHibernation persists the idle Chat hibernation preference.
func (s *Store) SetChatHibernation(ctx context.Context, policy domain.ChatHibernationPolicy, now time.Time) error {
	if !domain.ValidChatHibernationIdle(policy.WorkerIdle) || !domain.ValidChatHibernationIdle(policy.OrchestratorIdle) {
		return fmt.Errorf("invalid chat hibernation thresholds %s/%s", policy.WorkerIdle, policy.OrchestratorIdle)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetChatHibernation(ctx, gen.SetChatHibernationParams{
		Enabled:                 policy.Enabled,
		WorkerIdleSeconds:       int64(policy.WorkerIdle / time.Second),
		OrchestratorIdleSeconds: int64(policy.OrchestratorIdle / time.Second),
		UpdatedAt:               now,
	}); err != nil {
		return fmt.Errorf("set chat hibernation: %w", err)
	}
	return nil
}

// SetHarnessDefault updates one entry atomically so concurrent saves for other
// harnesses cannot overwrite each other. Empty values remove the override.
func (s *Store) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	path := `$."` + string(agent) + `"`
	if value.IsZero() {
		return s.qw.ResetHarnessDefault(ctx, gen.ResetHarnessDefaultParams{HarnessPath: path, UpdatedAt: now})
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return s.qw.SetHarnessDefault(ctx, gen.SetHarnessDefaultParams{HarnessPath: path, Config: string(data), UpdatedAt: now})
}

// decodeWorkflowDefaults reads the stored object over the built-in defaults, so
// a missing key, an empty object or unreadable text all give the default.
func decodeWorkflowDefaults(raw string) domain.WorkflowDefaults {
	d := domain.DefaultWorkflowDefaults()
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return domain.DefaultWorkflowDefaults()
	}
	return d.Normalized()
}

// WorkflowDefaults reads the app-wide workflow defaults.
func (s *Store) WorkflowDefaults(ctx context.Context) (domain.WorkflowDefaults, error) {
	row, err := s.qr.GetAppSettings(ctx)
	if err != nil {
		return domain.WorkflowDefaults{}, fmt.Errorf("read workflow defaults: %w", err)
	}
	return decodeWorkflowDefaults(row.WorkflowDefaults), nil
}

// SetWorkflowDefaults persists the app-wide workflow defaults.
func (s *Store) SetWorkflowDefaults(ctx context.Context, d domain.WorkflowDefaults, now time.Time) error {
	d = d.Normalized()
	data, err := json.Marshal(d)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.SetWorkflowDefaults(ctx, gen.SetWorkflowDefaultsParams{WorkflowDefaults: string(data), UpdatedAt: now}); err != nil {
		return fmt.Errorf("set workflow defaults: %w", err)
	}
	return nil
}
