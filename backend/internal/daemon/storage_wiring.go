package daemon

import (
	"context"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
	"tallpine.eu/tallpine/backend/internal/service/storageusage"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// newStorageUsage builds Settings > Storage over the cleanup engine. Removing
// one worktree goes through the same salvaging, guarded session cleanup as
// `tallpine session cleanup`.
func newStorageUsage(dataDir, runFile string, store *sqlite.Store, sessMgr any, cleaner storageusage.Cleaner) *storageusage.Service {
	cfg := storageusage.Config{
		DataDir: dataDir, StateRoot: filepath.Dir(runFile), Sessions: store, Cleaner: cleaner,
	}
	if cleaner0, ok := sessMgr.(sessionCleaner); ok {
		root := filepath.Join(dataDir, "worktrees")
		var prs cleanupsvc.PRLookup
		if store != nil {
			prs = store.ListPRsBySession
		}
		check := cleanupsvc.SalvagingSafetyCheck(root, nil, cleanupsvc.NewSalvager(dataDir, prs))
		remover := worktreeRemover{mgr: cleaner0}
		cfg.Worktrees = func(ctx context.Context, id domain.SessionID) (string, error) {
			out, err := remover.CleanupSession(ctx, id, check)
			return out.Kept, err
		}
	} else {
		cfg.Worktrees = func(context.Context, domain.SessionID) (string, error) {
			return "worktree removal is unavailable", nil
		}
	}
	return storageusage.New(cfg)
}
