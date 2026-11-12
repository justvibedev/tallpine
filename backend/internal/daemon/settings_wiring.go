package daemon

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// settingsStore adapts the SQLite store to the settings service's Store.
//
// The two define their own snapshot types so neither depends on the other's; this
// is the one place that knows both, keeping the translation in the wiring.
type settingsStore struct{ store *sqlite.Store }

var _ settingssvc.Store = settingsStore{}

func (s settingsStore) GetAppSettings(ctx context.Context) (settingssvc.Snapshot, error) {
	row, err := s.store.GetAppSettings(ctx)
	if err != nil {
		return settingssvc.Snapshot{}, err
	}
	return settingssvc.Snapshot{
		DefaultSessionMode:         row.DefaultSessionMode,
		HarnessDefaults:            row.HarnessDefaults,
		VersionRefresh:             row.VersionRefresh,
		DynamicOrchestratorContext: row.DynamicOrchestratorContext,
		StorageCompaction:          row.StorageCompaction,
		ProviderEventRetentionDays: row.ProviderEventRetentionDays,
		ChatHibernation:            row.ChatHibernation,
		WorkflowDefaults:           row.WorkflowDefaults,
		PeerCheck:                  row.PeerCheck,
		UpdatedAt:                  row.UpdatedAt,
	}, nil
}

func (s settingsStore) SetDefaultSessionMode(
	ctx context.Context,
	mode domain.SessionMode,
	now time.Time,
) error {
	return s.store.SetDefaultSessionMode(ctx, mode, now)
}

func (s settingsStore) SetDynamicOrchestratorContext(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetDynamicOrchestratorContext(ctx, enabled, now)
}

func (s settingsStore) SetVersionRefresh(ctx context.Context, enabled bool, now time.Time) error {
	return s.store.SetVersionRefresh(ctx, enabled, now)
}

func (s settingsStore) SetStorageCompaction(ctx context.Context, enabled bool, retentionDays int, now time.Time) error {
	return s.store.SetStorageCompaction(ctx, enabled, retentionDays, now)
}

func (s settingsStore) SetChatHibernation(ctx context.Context, policy domain.ChatHibernationPolicy, now time.Time) error {
	return s.store.SetChatHibernation(ctx, policy, now)
}

func (s settingsStore) SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault, now time.Time) error {
	return s.store.SetHarnessDefault(ctx, agent, value, now)
}

func (s settingsStore) SetWorkflowDefaults(ctx context.Context, d domain.WorkflowDefaults, now time.Time) error {
	return s.store.SetWorkflowDefaults(ctx, d, now)
}
