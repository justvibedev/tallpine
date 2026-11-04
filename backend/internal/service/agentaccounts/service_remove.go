package agentaccounts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// removeFolder deletes an account folder Tallpine made. Links inside it are removed,
// never followed, so the computer's own history is untouched.
func (s *Service) removeFolder(home string) {
	if s.insideRoot(home) {
		_ = os.RemoveAll(home)
	}
}

// signOutLocally clears a saved account's folder without the CLI: it deletes
// the folder's own credentials file and marks the account signed out until it
// signs in again. A login kept in the Keychain stays there.
func (s *Service) signOutLocally(account domain.AgentAccount) {
	s.removeCredentialsFile(account)
	s.mu.Lock()
	if s.signedOutLocally == nil {
		s.signedOutLocally = map[string]bool{}
	}
	s.signedOutLocally[account.ID] = true
	delete(s.readings, account.ID)
	s.mu.Unlock()
}

// removeCredentialsFile deletes the account folder's own credentials file. The
// folder must be a real directory under the accounts root, not a link; a link
// in place of the file is removed itself, never followed; anything else is left.
func (s *Service) removeCredentialsFile(account domain.AgentAccount) {
	if !s.insideRoot(account.Home) {
		return
	}
	if info, err := os.Lstat(account.Home); err != nil || !info.IsDir() {
		s.log.Warn("agent account folder is not a plain directory; credentials left", "account", account.ID)
		return
	}
	path := filepath.Join(account.Home, ".credentials.json")
	info, err := os.Lstat(path)
	if err != nil {
		return
	}
	if info.Mode().Type() != 0 && info.Mode()&os.ModeSymlink == 0 {
		s.log.Warn("agent account credentials entry is not a file; left", "account", account.ID)
		return
	}
	_ = os.Remove(path) // a link is removed itself, not its target
}

// insideRoot reports whether home is a folder Tallpine made under its accounts root
// (never the computer's own folder).
func (s *Service) insideRoot(home string) bool {
	// The root may now be reached through another spelling (a symlinked data
	// directory) than when the account was made; compare where both resolve.
	root, home2 := s.root, home
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home2 = h
	}
	rel, err := filepath.Rel(root, home2)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Remove deletes an account folder, signing it out through the CLI only for
// providers that allow it (not Claude: see ports.SignsOutOnRemove). Sessions
// on it start on the provider's default at their next launch.
func (s *Service) Remove(ctx context.Context, accountID string) error {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Device() {
		return ports.ErrAgentAccountDeviceFixed
	}
	// From here no launch resolves to the account; then its agents stop, so
	// none runs on a login Tallpine deletes. An agent that cannot be stopped keeps the
	// account, usable again.
	s.mu.Lock()
	s.removing[accountID] = true
	stop := s.stopAgents
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.removing, accountID)
		s.mu.Unlock()
	}()
	if stop != nil {
		if err := stop(ctx, accountID); err != nil {
			return fmt.Errorf("%w: %w", ports.ErrAgentAccountInUse, err)
		}
	}
	if provider := s.providers[account.Provider]; ports.SignsOutOnRemove(provider) {
		if err := provider.Logout(ctx, account.Home); err != nil {
			s.log.Warn("sign out removed account", "account", accountID, "error", err)
		}
	}
	if err := s.store.DeleteAgentAccount(ctx, accountID); err != nil {
		return err
	}
	s.removeFolder(account.Home)
	s.mu.Lock()
	delete(s.readings, accountID)
	s.mu.Unlock()
	s.notifyDefaultChanged(account.Provider)
	return nil
}

// ConsumeResetCredit spends one Codex usage-limit reset of the account.
func (s *Service) ConsumeResetCredit(ctx context.Context, accountID, idempotencyKey string) (domain.CodexResetCreditOutcome, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return "", err
	}
	crediter, ok := s.providers[account.Provider].(resetCrediter)
	if !ok {
		return "", ports.ErrAgentAccountProviderMismatch
	}
	outcome, err := crediter.ConsumeResetCredit(ctx, account.Home, idempotencyKey)
	if err == nil {
		s.read(ctx, account)
	}
	return outcome, err
}
