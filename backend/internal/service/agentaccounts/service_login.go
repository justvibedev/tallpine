package agentaccounts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/shellterm"
)

// LoginStart is a sign-in running in an embedded terminal.
type LoginStart struct {
	OperationID string
	Provider    domain.AgentAccountProvider
	Terminal    shellterm.ShellTerminal
}

// StartLogin creates a new account folder and runs the provider's own sign-in
// for it in a terminal.
func (s *Service) StartLogin(ctx context.Context, p domain.AgentAccountProvider) (LoginStart, error) {
	return s.startLogin(ctx, p, nil)
}

// StartSignInAgain runs the provider's sign-in in a saved account's own folder,
// so a signed-out account comes back with its sessions, history and settings.
func (s *Service) StartSignInAgain(ctx context.Context, accountID string) (LoginStart, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return LoginStart{}, err
	}
	if account.Device() {
		return LoginStart{}, ports.ErrAgentAccountDeviceFixed
	}
	return s.startLogin(ctx, account.Provider, &account)
}

func (s *Service) startLogin(ctx context.Context, p domain.AgentAccountProvider, account *domain.AgentAccount) (LoginStart, error) {
	provider := s.providers[p]
	s.mu.Lock()
	terminals := s.terminals
	s.mu.Unlock()
	if provider == nil || terminals == nil {
		return LoginStart{}, ports.ErrAgentAccountProviderMismatch
	}
	id := uuid.NewString()
	home := filepath.Join(s.root, string(p), id)
	title := map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "Add Claude account", domain.AgentAccountProviderCodex: "Add Codex account"}[p]
	if account != nil {
		home = account.Home
		title = map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "Sign in to Claude account", domain.AgentAccountProviderCodex: "Sign in to Codex account"}[p]
	}
	// A saved account's folder is never removed here; only a new one is.
	discard := func() {
		if account == nil {
			_ = os.RemoveAll(home)
		}
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return LoginStart{}, fmt.Errorf("create account folder: %w", err)
	}
	// Linking first means the sign-in never creates a private copy of a shared folder.
	if err := s.prepare(provider, home); err != nil {
		discard()
		return LoginStart{}, err
	}
	argv, err := provider.LoginArgv(ctx)
	if err != nil {
		discard()
		return LoginStart{}, err
	}
	terminal, err := terminals.OpenCommandTerminal(ctx, shellterm.OpenCommandTerminalInput{Argv: argv, Env: provider.LaunchEnv(home), WorkingDir: home, Title: title})
	if err != nil {
		discard()
		return LoginStart{}, fmt.Errorf("open sign-in terminal: %w", err)
	}
	s.mu.Lock()
	s.logins[id] = &login{id: id, provider: p, home: home, terminal: terminal.HandleID, account: account}
	s.mu.Unlock()
	return LoginStart{OperationID: id, Provider: p, Terminal: terminal}, nil
}

// LoginStatus is how a sign-in stands.
type LoginStatus string

// Sign-in states.
const (
	LoginWaiting   LoginStatus = "waiting"
	LoginComplete  LoginStatus = "complete"
	LoginDuplicate LoginStatus = "duplicate"
	// LoginMismatch is a sign-in again with another login than the account's.
	LoginMismatch LoginStatus = "mismatch"
)

// VerifyLogin checks whether the sign-in finished, and saves the account when
// it did. A login that is already an account, the computer's own included, is
// refused so one login never sits in two folders, each renewing its own token.
func (s *Service) VerifyLogin(ctx context.Context, operationID string) (LoginStatus, *AccountView, error) {
	s.mu.Lock()
	op := s.logins[operationID]
	s.mu.Unlock()
	if op == nil {
		return "", nil, ports.ErrAgentAccountLoginUnknown
	}
	provider := s.providers[op.provider]
	identity, err := provider.Identity(ctx, op.home)
	if err != nil {
		return "", nil, fmt.Errorf("check sign-in: %w", err)
	}
	if !identity.SignedIn {
		return LoginWaiting, nil, nil
	}
	views, err := s.List(ctx, false)
	if err != nil {
		return "", nil, err
	}
	// Claim the sign-in before acting on it. A cancel, or a second check, that
	// ran during the CLI calls above has already ended it, and saving now would
	// keep an account whose folder is gone.
	if !s.claimLogin(op) {
		return "", nil, ports.ErrAgentAccountLoginUnknown
	}
	for _, view := range views {
		if op.account != nil && view.Account.ID == op.account.ID {
			continue
		}
		if view.Account.Provider == op.provider && view.SignedIn && identity.SameLogin(view.Account.Email, view.Account.OrgID) {
			s.endLogin(ctx, op, true)
			return LoginDuplicate, &view, nil
		}
	}
	if op.account != nil {
		return s.finishSignInAgain(ctx, op, identity, views)
	}
	account := domain.AgentAccount{ID: op.id, Provider: op.provider, Home: op.home, Email: identity.Email, Plan: identity.Plan, OrgID: identity.OrgID, OrgName: identity.OrgName, CreatedAt: s.now()}
	if err := s.store.CreateAgentAccount(ctx, account); err != nil {
		s.releaseLogin(op) // still pending: a later check can save it
		return "", nil, err
	}
	if err := provider.Prepare(op.home); err != nil {
		s.log.Warn("prepare account folder", "account", op.id, "error", err)
	}
	s.endLogin(ctx, op, false)
	s.read(ctx, account)
	s.notifyDefaultChanged(account.Provider)
	views, err = s.List(ctx, false)
	if err != nil {
		return "", nil, err
	}
	for _, view := range views {
		if view.Account.ID == account.ID {
			return LoginComplete, &view, nil
		}
	}
	return LoginComplete, nil, nil
}

// claimLogin takes op out of the pending sign-ins if it is still there, so
// exactly one caller goes on to save, refuse or cancel it.
func (s *Service) claimLogin(op *login) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logins[op.id] != op {
		return false
	}
	delete(s.logins, op.id)
	return true
}

// releaseLogin puts a claimed sign-in back after a failure that left it intact.
func (s *Service) releaseLogin(op *login) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins[op.id] = op
}

// finishSignInAgain keeps a saved account's folder signed in only as the login
// it was saved with: its sessions and limits belong to that login.
func (s *Service) finishSignInAgain(ctx context.Context, op *login, identity domain.AgentAccountIdentityCheck, views []AccountView) (LoginStatus, *AccountView, error) {
	var saved *AccountView
	for i := range views {
		if views[i].Account.ID == op.account.ID {
			saved = &views[i]
		}
	}
	if op.account.Email != "" && !identity.SameLogin(op.account.Email, op.account.OrgID) {
		s.endLogin(ctx, op, true)
		return LoginMismatch, saved, nil
	}
	s.endLogin(ctx, op, false)
	s.mu.Lock()
	delete(s.signedOutLocally, op.account.ID)
	s.mu.Unlock()
	s.read(ctx, *op.account)
	s.notifyDefaultChanged(op.account.Provider)
	views, err := s.List(ctx, false)
	if err != nil {
		return "", nil, err
	}
	for _, view := range views {
		if view.Account.ID == op.account.ID {
			return LoginComplete, &view, nil
		}
	}
	return LoginComplete, nil, nil
}

// CancelLogin stops a sign-in and removes its unsaved folder. A saved
// account's folder stays as it is.
func (s *Service) CancelLogin(ctx context.Context, operationID string) error {
	s.mu.Lock()
	op := s.logins[operationID]
	s.mu.Unlock()
	if op == nil || !s.claimLogin(op) {
		return ports.ErrAgentAccountLoginUnknown
	}
	s.endLogin(ctx, op, op.account == nil)
	return nil
}

// endLogin closes a sign-in. discard signs its folder out, so a refused login's
// token does not outlive the attempt, and removes the folder unless it belongs
// to a saved account.
func (s *Service) endLogin(ctx context.Context, op *login, discard bool) {
	s.mu.Lock()
	delete(s.logins, op.id)
	s.mu.Unlock()
	s.mu.Lock()
	terminals := s.terminals
	s.mu.Unlock()
	if op.terminal != "" && terminals != nil {
		_ = terminals.CloseShellTerminal(context.WithoutCancel(ctx), op.terminal)
	}
	if discard {
		// A folder that is about to be deleted is signed out only by providers
		// whose sign-out is local to it (see SignsOutOnRemove). A saved
		// account's folder stays, so a refused login in it must be signed out.
		provider := s.providers[op.provider]
		if op.account != nil && !ports.SignsOutOnRemove(provider) {
			// A saved account's folder stays and now holds a login that is not
			// its own. The CLI's sign-out would also revoke that login's tokens on
			// the server, and it may be the computer's own login, so clear the
			// folder locally instead.
			s.signOutLocally(*op.account)
		}
		if ports.SignsOutOnRemove(provider) {
			logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
			defer cancel()
			if err := provider.Logout(logoutCtx, op.home); err != nil {
				s.log.Debug("sign out refused account folder", "provider", op.provider, "error", err)
			}
		}
		if op.account == nil {
			s.removeFolder(op.home)
		}
	}
}
