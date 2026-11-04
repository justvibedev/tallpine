package codex

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// PruneStaleRows deletes managed Codex account rows whose folder under root is
// gone: a login that crashed after its row was saved and before its folder
// moved in. Run it at startup, before any login can be in flight. It returns
// the number of rows removed.
func PruneStaleRows(ctx context.Context, root string, store ports.CodexAccountRegistryStore, logger *slog.Logger) (int, error) {
	if root == "" {
		return 0, nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	accounts, err := store.ListAgentAccounts(ctx)
	if err != nil {
		return 0, fmt.Errorf("list agent accounts: %w", err)
	}
	removed := 0
	for _, account := range accounts {
		if account.Provider != domain.AgentAccountProviderCodex || !account.Managed() || !isCanonicalUUIDv4(account.ID) {
			continue
		}
		if _, statErr := os.Lstat(filepath.Join(root, account.ID)); !os.IsNotExist(statErr) {
			continue
		}
		if err := store.DeleteAgentAccount(ctx, account.ID); err != nil {
			return removed, fmt.Errorf("remove stale Codex account %s: %w", account.ID, err)
		}
		removed++
		logger.Info("removed Codex account row whose folder is missing", "account", account.ID)
	}
	return removed, nil
}
