// Package officialimport exposes the Agent Orchestrator -> Tallpine project
// migration through the daemon service boundary.
package officialimport

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/officialimport"
	"tallpine.eu/tallpine/backend/internal/service/importer"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// stagingSubdir holds disposable snapshots of the official database. It lives
// under this product's data dir so nothing of the import is kept elsewhere.
const stagingSubdir = "import-staging"

// SettingsService is the slice of the daemon preference service the import
// writes global settings through.
type SettingsService interface {
	Get(ctx context.Context) (settingssvc.Snapshot, error)
	SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) (settingssvc.Snapshot, error)
	SetVersionRefresh(ctx context.Context, enabled bool) (settingssvc.Snapshot, error)
	SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault) (settingssvc.Snapshot, error)
}

// RunInput configures one import.
type RunInput struct {
	// SourceDataDir overrides the official data dir (default ~/.ao/data).
	SourceDataDir   string
	DryRun          bool
	ProjectIDs      []string
	IncludeSettings bool
}

// Service is the controller-facing contract.
type Service interface {
	Run(ctx context.Context, in RunInput) (officialimport.Report, error)
}

// Deps bundles the service dependencies.
type Deps struct {
	Store         officialimport.Target
	Settings      SettingsService
	TargetDataDir string
}

// Manager implements Service over the daemon's live store.
type Manager struct{ deps Deps }

var _ Service = (*Manager)(nil)

// New constructs the import service.
func New(deps Deps) *Manager { return &Manager{deps: deps} }

// Run snapshots the official database and plans or applies the import.
func (m *Manager) Run(ctx context.Context, in RunInput) (officialimport.Report, error) {
	source := strings.TrimSpace(in.SourceDataDir)
	if source == "" {
		def, err := brand.OfficialDataDir()
		if err != nil {
			return officialimport.Report{}, err
		}
		source = def
	}
	source, err := importer.ResolveDataDir(source)
	if err != nil {
		return officialimport.Report{}, err
	}
	target, err := importer.ResolveDataDir(m.deps.TargetDataDir)
	if err != nil {
		return officialimport.Report{}, fmt.Errorf("resolve target data dir: %w", err)
	}
	same, err := importer.SameDataDir(source, target)
	if err != nil {
		return officialimport.Report{}, err
	}
	if same {
		return officialimport.Report{}, apierr.Invalid("AO_IMPORT_SOURCE_IS_TARGET",
			"the source data dir is this install's own data dir", map[string]any{"path": source})
	}

	var settings officialimport.SettingsTarget
	if m.deps.Settings != nil {
		settings = settingsAdapter{m.deps.Settings}
	}
	rep, err := officialimport.RunWithSettings(ctx, m.deps.Store, settings, officialimport.Options{
		SourceDataDir:   source,
		StagingDir:      filepath.Join(target, stagingSubdir),
		DryRun:          in.DryRun,
		ProjectIDs:      in.ProjectIDs,
		IncludeSettings: in.IncludeSettings,
	})
	var unknown *officialimport.UnknownProjectError
	if errors.As(err, &unknown) {
		return rep, apierr.Invalid("AO_IMPORT_UNKNOWN_PROJECT", unknown.Error(), map[string]any{"ids": unknown.IDs})
	}
	return rep, err
}

type settingsAdapter struct{ svc SettingsService }

func (a settingsAdapter) Current(ctx context.Context) (officialimport.TargetSettings, error) {
	snap, err := a.svc.Get(ctx)
	if err != nil {
		return officialimport.TargetSettings{}, err
	}
	return officialimport.TargetSettings{
		DefaultSessionMode: snap.DefaultSessionMode,
		VersionRefresh:     snap.VersionRefresh,
		HarnessDefaults:    snap.HarnessDefaults,
	}, nil
}

func (a settingsAdapter) SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) error {
	_, err := a.svc.SetDefaultSessionMode(ctx, mode)
	return err
}

func (a settingsAdapter) SetVersionRefresh(ctx context.Context, enabled bool) error {
	_, err := a.svc.SetVersionRefresh(ctx, enabled)
	return err
}

func (a settingsAdapter) SetHarnessDefault(ctx context.Context, h domain.AgentHarness, v domain.HarnessDefault) error {
	_, err := a.svc.SetHarnessDefault(ctx, h, v)
	return err
}
