// Package devimport exposes developer project-registry import operations
// through the daemon service boundary.
package devimport

import (
	"context"
	"fmt"

	engine "tallpine.eu/tallpine/backend/internal/devimport"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/service/importer"
)

// Store is the live target store used by the daemon.
type Store = engine.Store

// SourceStore is an imported Tallpine store opened read-only for the duration of one
// import run.
type SourceStore interface {
	engine.Store
	Close() error
}

// SourceOpener opens a Tallpine data directory as a source store.
type SourceOpener func(ctx context.Context, dataDir string) (SourceStore, error)

// RunInput configures one project-registry import.
type RunInput struct {
	SourceDataDir string
	DryRun        bool
}

// Service is the controller-facing dev import contract.
type Service interface {
	RunProjects(ctx context.Context, in RunInput) (engine.Report, error)
}

// Deps bundles the service dependencies.
type Deps struct {
	Store         Store
	TargetDataDir string
	OpenSource    SourceOpener
}

// Manager implements Service over the daemon's live store.
type Manager struct {
	store         Store
	targetDataDir string
	openSource    SourceOpener
}

var _ Service = (*Manager)(nil)

// New constructs the dev import service.
func New(deps Deps) *Manager {
	return &Manager{store: deps.Store, targetDataDir: deps.TargetDataDir, openSource: deps.OpenSource}
}

// RunProjects reads the source Tallpine database read-only and plans or writes into
// the daemon's live store.
func (m *Manager) RunProjects(ctx context.Context, in RunInput) (engine.Report, error) {
	sourceDataDir, err := importer.ResolveDataDir(in.SourceDataDir)
	if err != nil {
		return engine.Report{}, err
	}
	targetDataDir, err := importer.ResolveDataDir(m.targetDataDir)
	if err != nil {
		return engine.Report{}, fmt.Errorf("resolve target data dir: %w", err)
	}
	same, err := importer.SameDataDir(sourceDataDir, targetDataDir)
	if err != nil {
		return engine.Report{}, err
	}
	if same {
		return engine.Report{}, apierr.Invalid("DEV_IMPORT_SOURCE_TARGET_SAME",
			"sourceDataDir must be different from the target Tallpine data dir", map[string]any{"path": sourceDataDir})
	}
	if m.openSource == nil {
		return engine.Report{}, fmt.Errorf("open source store: dependency is nil")
	}

	source, err := m.openSource(ctx, sourceDataDir)
	if err != nil {
		return engine.Report{}, fmt.Errorf("open source store: %w", err)
	}
	defer func() { _ = source.Close() }()

	return engine.Run(ctx, source, m.store, engine.Options{
		SourceDataDir: sourceDataDir,
		TargetDataDir: targetDataDir,
		DryRun:        in.DryRun,
	})
}
