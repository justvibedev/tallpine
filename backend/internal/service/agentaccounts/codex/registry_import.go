package codex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// RegistryImport counts what ImportRegistry did with the file registry.
type RegistryImport struct {
	Imported int
	Existing int
	Skipped  int
}

// ImportRegistry copies the accounts of the Codex file registry under root
// into the agent account store, once per account. It keeps each account id,
// is safe to run again (accounts already in the store are left alone) and only
// reads the registry: the old files stay in place, read-only, for one release.
// Broken registry entries are skipped and logged, never imported.
func ImportRegistry(ctx context.Context, root string, store ports.AgentAccountStore, logger *slog.Logger) (RegistryImport, error) {
	var result RegistryImport
	if logger == nil {
		logger = slog.Default()
	}
	if root == "" {
		return result, nil
	}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return result, nil
	}
	catalog := newCodexAccountCatalog(root, logger)
	entries, err := os.ReadDir(root)
	if err != nil {
		return result, fmt.Errorf("read Codex account registry: %w", err)
	}
	var records []codexAccountRecord
	for _, entry := range entries {
		id := entry.Name()
		if !isCanonicalUUIDv4(id) {
			continue
		}
		descriptor, readErr := readCodexAccountDescriptor(filepath.Join(root, id, codexAccountDescriptorFilename))
		if readErr == nil && descriptor.Version != 1 && descriptor.Version != codexAccountVersion {
			readErr = errors.New("unsupported descriptor version")
		}
		records = append(records, catalog.inspectManaged(id, descriptor, readErr))
	}
	saved, err := store.ListAgentAccounts(ctx)
	if err != nil {
		return result, fmt.Errorf("list agent accounts: %w", err)
	}
	byID, byHome := make(map[string]bool, len(saved)), make(map[string]bool, len(saved))
	for _, account := range saved {
		byID[account.ID] = true
		byHome[account.Home] = true
	}
	for _, record := range records {
		id := record.Snapshot.ID
		switch {
		case byID[id]:
			result.Existing++
			logger.Debug("codex registry account already in the account store", "account", id)
			continue
		case record.Snapshot.Status == domain.CodexAccountStatusBroken:
			result.Skipped++
			logger.Warn("codex registry account not imported: entry is broken", "account", id, "reason", record.Snapshot.ReasonCode)
			continue
		case byHome[record.Home]:
			result.Skipped++
			logger.Warn("codex registry account not imported: its home belongs to another saved account", "account", id)
			continue
		}
		account := accountOf(codexAccountDescriptor{
			ID: id, Source: record.Snapshot.Source, AuthMethod: record.Snapshot.AuthMethod,
			AccountEmail: record.Snapshot.AccountEmail, ProviderAccountID: record.ProviderAccountID,
			CreatedAt: record.CreatedAt, VerifiedAt: record.VerifiedAt,
		}, record.Home)
		if err := store.CreateAgentAccount(ctx, account); err != nil {
			return result, fmt.Errorf("import Codex account %s: %w", id, err)
		}
		byID[id], byHome[record.Home] = true, true
		result.Imported++
		logger.Info("imported Codex registry account into the account store", "account", id, "source", account.Source)
	}
	return result, nil
}
