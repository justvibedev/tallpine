// Package agentaccounts manages the Claude Code and Codex subscription accounts
// Tallpine launches agents with. An account is a config folder of the provider's own
// CLI; the computer's own folder is the "This computer" account. A session is
// pinned to one account per provider and moves by restarting its agent there.
package agentaccounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/shellterm"
)

// freshFor is how long a read of an account's identity and limits is reused.
const freshFor = 5 * time.Minute

// retryEmptyAfter is how long an answer with no limit windows is reused. A
// folder read just after its sign-in can answer before its limits are known.
const retryEmptyAfter = 30 * time.Second

// recheckEvery is the least time between two session-triggered reads of one
// account. Each read launches the provider CLI twice (identity, then a usage
// request), so thirty sessions refused at once must not mean thirty reads.
const recheckEvery = 30 * time.Second

// Terminals opens the embedded terminal a sign-in runs in.
type Terminals interface {
	OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error)
	CloseShellTerminal(context.Context, string) error
}

// resetCrediter is the Codex-only ability to spend a usage-limit reset.
type resetCrediter interface {
	ConsumeResetCredit(ctx context.Context, home, idempotencyKey string) (domain.CodexResetCreditOutcome, error)
}

// Service is the account catalog, its cached readings, and the per-session
// launch environment.
type Service struct {
	// splitWarned holds the accounts whose split history a launch already logged.
	splitWarned sync.Map
	store       ports.AgentAccountStore
	providers   map[domain.AgentAccountProvider]ports.AgentAccountProviderCLI
	root        string // account folders live in root/<provider>/<id>
	terminals   Terminals
	// stopAgents stops every running agent on an account before it is removed.
	stopAgents func(context.Context, string) error
	log        *slog.Logger
	now        func() time.Time

	mu       sync.Mutex
	readings map[string]*reading
	logins   map[string]*login
	// removing holds accounts being removed. No launch resolves to one, so an
	// agent cannot start on a login Tallpine is about to delete.
	removing map[string]bool
	// signedOutLocally holds saved accounts whose folder was cleared after a
	// refused re-sign-in, until they sign in again. See signOutLocally.
	signedOutLocally map[string]bool
	defaultChanged   func(domain.AgentAccountProvider)
	placement        func() domain.AccountPlacement
	// recentPlacements are when placement last chose each account, to spread a
	// burst of spawns before readings show the new sessions.
	recentPlacements map[string][]time.Time
	// rechecks throttles RecheckSessionAccount per account.
	rechecks     map[string]*recheckState
	recheckEvery time.Duration
}

// recheckState is one account's session-triggered reads: at most one runs, and
// requests that arrive while one runs or too soon after it share one more read.
type recheckState struct {
	running bool
	pending bool
	last    time.Time // wall clock with monotonic reading; not Service.now
}

// reading is the last identity and limits read for one account.
type reading struct {
	identity    domain.AgentAccountIdentityCheck
	identityErr string
	// mismatch: the folder is signed in, but as another login than its own.
	mismatch  bool
	limits    *domain.AgentAccountLimits
	limitsErr string
	at        time.Time
}

// knownUnusable reports a reading that shows the folder cannot launch as its
// account: logged in by no method at all, or signed in as another login. An
// API key is not a subscription, but the CLI launches with it, so it passes. A
// failed probe shows nothing, and no reading yet is not known.
func (r *reading) knownUnusable() bool {
	return r != nil && (r.mismatch || (r.identityErr == "" && !r.identity.LoggedIn))
}

type login struct {
	id       string
	provider domain.AgentAccountProvider
	home     string
	terminal string
	// account is the saved account a sign-in again runs in; nil for a new one.
	account *domain.AgentAccount
}

// New builds the service. root must be absolute; the account folders' paths
// are stored exactly, because Claude keys each folder's login on the path text.
func New(store ports.AgentAccountStore, providers []ports.AgentAccountProviderCLI, root string, terminals Terminals, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	byProvider := map[domain.AgentAccountProvider]ports.AgentAccountProviderCLI{}
	for _, p := range providers {
		byProvider[p.Provider()] = p
	}
	return &Service{
		store: store, providers: byProvider, root: filepath.Clean(root), terminals: terminals, log: log,
		now: func() time.Time { return time.Now().UTC() }, readings: map[string]*reading{}, logins: map[string]*login{}, removing: map[string]bool{},
		rechecks: map[string]*recheckState{}, recheckEvery: recheckEvery,
	}
}

// SetTerminals late-binds the terminal sign-ins run in, which the daemon builds
// after Session Manager.
func (s *Service) SetTerminals(terminals Terminals) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminals = terminals
}

// SetAgentStopper late-binds what stops the agents on an account before it is
// removed, so none keeps running on a login Tallpine deletes.
func (s *Service) SetAgentStopper(stop func(ctx context.Context, accountID string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopAgents = stop
}

// AccountView is an account with its latest readings, as the app shows it.
type AccountView struct {
	Account     domain.AgentAccount
	Default     bool
	SignedIn    bool
	IdentityErr string
	Limits      *domain.AgentAccountLimits
	LimitsErr   string
	// HistorySplit names the shared folders the account keeps its own copy of;
	// empty when it shares them all.
	HistorySplit []string
	// DuplicateOf is the id of an earlier entry holding the same login (same
	// email and organization); empty when this entry is the only one.
	DuplicateOf string
}

// List returns every account, the computer's own first per provider. refresh
// reads identity and limits again even when the last reading is fresh.
func (s *Service) List(ctx context.Context, refresh bool) ([]AccountView, error) {
	accounts, err := s.accounts(ctx)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	for _, account := range accounts {
		if s.needsReading(account.ID, refresh) {
			wg.Go(func() { s.read(ctx, account) })
		}
	}
	wg.Wait()
	views := make([]AccountView, 0, len(accounts))
	for _, account := range accounts {
		def, _ := s.defaultID(ctx, account.Provider)
		s.mu.Lock()
		r := s.readings[account.ID]
		s.mu.Unlock()
		view := AccountView{Account: account, Default: def == account.ID, HistorySplit: s.historySplit(account)}
		if r != nil {
			view.SignedIn, view.IdentityErr, view.Limits, view.LimitsErr = r.identity.SignedIn, r.identityErr, r.limits, r.limitsErr
			if account.Device() {
				view.Account.Email, view.Account.Plan = r.identity.Email, r.identity.Plan
			} else if r.identity.Plan != "" {
				view.Account.Plan = r.identity.Plan
			}
			if view.SignedIn && !r.mismatch && (account.Device() || account.OrgID == "") {
				view.Account.OrgID, view.Account.OrgName = r.identity.OrgID, r.identity.OrgName
			}
		}
		views = append(views, view)
	}
	markDuplicates(views)
	return views, nil
}

// mismatchMessage says which login a folder holds instead of the saved one.
// When the emails match only the organizations differ, so it names those.
func mismatchMessage(identity domain.AgentAccountIdentityCheck, account domain.AgentAccount) string {
	if strings.EqualFold(identity.Email, account.Email) {
		return fmt.Sprintf("the folder is now signed in to %s, not %s", orgName(identity.OrgName, identity.OrgID), orgName(account.OrgName, account.OrgID))
	}
	return fmt.Sprintf("the folder is now signed in as %s, not %s", identity.Email, account.Email)
}

func orgName(name, id string) string {
	if name != "" {
		return name
	}
	if id != "" {
		return "organization " + id
	}
	return "another organization"
}

// markDuplicates flags every signed-in entry that holds the same login as an
// earlier one of its provider: both read the same limits, so they are one
// account listed twice.
func markDuplicates(views []AccountView) {
	for i := range views {
		if !views[i].SignedIn {
			continue
		}
		for j := range i {
			a, b := views[j].Account, views[i].Account
			if views[j].SignedIn && a.Provider == b.Provider && domain.SameAgentLogin(a.Email, a.OrgID, b.Email, b.OrgID) {
				views[i].DuplicateOf = a.ID
				break
			}
		}
	}
}

// SetLabel names a saved account, or clears the name with an empty label. The
// computer's own login has no saved entry to name.
func (s *Service) SetLabel(ctx context.Context, accountID, label string) error {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Device() {
		return ports.ErrAgentAccountDeviceLabel
	}
	return s.store.SetAgentAccountLabel(ctx, accountID, strings.TrimSpace(label))
}

func (s *Service) needsReading(id string, refresh bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.readings[id]
	if refresh || r == nil {
		return true
	}
	ttl := freshFor
	if r.limits != nil && len(r.limits.Windows) == 0 {
		// A plan that never reports windows is then read on every poll, one CLI call a minute.
		ttl = retryEmptyAfter
	}
	return s.now().Sub(r.at) > ttl
}

// read asks the provider's CLI for the account's identity and limits.
func (s *Service) read(ctx context.Context, account domain.AgentAccount) *reading {
	provider := s.providers[account.Provider]
	if provider == nil {
		return nil
	}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	r := &reading{at: s.now()}
	identity, err := provider.Identity(ctx, account.Home)
	s.mu.Lock()
	cleared := s.signedOutLocally[account.ID]
	s.mu.Unlock()
	if cleared && err == nil {
		// The folder's own credentials were removed after a refused login; what
		// the CLI may still find elsewhere (the Keychain) is another login's.
		identity.SignedIn, identity.LoggedIn = false, false
	}
	r.identity = identity
	if err != nil {
		r.identityErr = err.Error()
		// A failed check (a timeout under load) says nothing about the login:
		// keep what the last good one found, so it does not read as signed out.
		s.mu.Lock()
		if prev := s.readings[account.ID]; prev != nil && prev.identityErr == "" {
			r.identity, r.limits = prev.identity, prev.limits
		}
		s.mu.Unlock()
	}
	switch {
	case err != nil:
	case !identity.SignedIn:
		r.limitsErr = "signed out"
	case !account.Device() && account.Email != "" && !identity.SameLogin(account.Email, account.OrgID):
		// The right-account check: the folder must still hold the login it was added with.
		r.identity.SignedIn = false
		r.mismatch = true
		r.identityErr = mismatchMessage(identity, account)
	default:
		if !account.Device() && account.OrgID == "" && identity.OrgID != "" {
			// An entry saved before organizations were recorded learns its own.
			if err := s.store.UpdateAgentAccountOrg(ctx, account.ID, identity.OrgID, identity.OrgName); err != nil {
				s.log.Warn("record account organization", "account", account.ID, "error", err)
			}
		}
		if limits, limitsErr := provider.Limits(ctx, account.Home); limitsErr != nil {
			r.limitsErr = limitsErr.Error()
		} else {
			r.limits = &limits
		}
	}
	// A caller that went away (a closed request, a cancelled move) says nothing
	// about the account; caching its failure would refuse the account for the
	// rest of the cache window.
	if caller.Err() != nil {
		return r
	}
	s.mu.Lock()
	s.readings[account.ID] = r
	s.mu.Unlock()
	return r
}

// accounts is the computer's own account per provider, then the saved ones.
func (s *Service) accounts(ctx context.Context) ([]domain.AgentAccount, error) {
	saved, err := s.store.ListAgentAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AgentAccount, 0, len(saved)+len(domain.AgentAccountProviders))
	for _, p := range domain.AgentAccountProviders {
		if s.providers[p] == nil {
			continue
		}
		out = append(out, domain.AgentAccount{ID: domain.DeviceAgentAccountID(p), Provider: p})
		for _, account := range saved {
			if account.Provider == p && !account.Managed() {
				out = append(out, account)
			}
		}
	}
	return out, nil
}

func (s *Service) find(ctx context.Context, id string) (domain.AgentAccount, error) {
	accounts, err := s.accounts(ctx)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	for _, account := range accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return domain.AgentAccount{}, ports.ErrAgentAccountUnknown
}

// defaultID is the provider's account for new sessions; the computer's own
// when none was picked or the picked one was removed.
func (s *Service) defaultID(ctx context.Context, p domain.AgentAccountProvider) (string, error) {
	id, ok, err := s.store.GetAgentAccountDefault(ctx, p)
	if err != nil || !ok {
		return domain.DeviceAgentAccountID(p), err
	}
	switch _, err := s.find(ctx, id); {
	case errors.Is(err, ports.ErrAgentAccountUnknown) || s.beingRemoved(id):
		return domain.DeviceAgentAccountID(p), nil // the picked default was removed
	case err != nil:
		return "", err
	}
	return id, nil
}

func (s *Service) beingRemoved(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removing[id]
}
