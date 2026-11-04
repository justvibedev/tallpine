package ports

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Agent account errors the API maps to client responses.
var (
	ErrAgentAccountUnknown          = errors.New("agent account not found")
	ErrAgentAccountProviderMismatch = errors.New("the account belongs to another provider")
	ErrAgentAccountSignedOut        = errors.New("the account's config folder is not signed in")
	ErrAgentAccountDuplicate        = errors.New("that login is already an account")
	ErrAgentAccountLoginUnknown     = errors.New("agent account sign-in not found")
	ErrAgentAccountDeviceLabel      = errors.New("the computer's own login cannot be named here")
	ErrAgentAccountDeviceFixed      = errors.New("the computer's own login cannot be removed here")
	ErrAgentAccountUnchanged        = errors.New("the session already uses this account")
	ErrAgentAccountChanged          = errors.New("the session's account changed since this turn")
	ErrAgentAccountUnshared         = errors.New("the account's folder cannot link to this computer's history")
	ErrAgentAccountSplitHistory     = errors.New("the account's folder keeps its own history")
	ErrAgentAccountInUse            = errors.New("an agent that uses the account could not be stopped")
	ErrAgentAccountDefaultUnusable  = errors.New("the default account is signed out or now holds another login")
)

// SplitHistoryError says an account folder holds real, non-empty copies of
// folders it should share with the computer's own, so conversations started on
// other accounts cannot be resumed there. Its text is shown to the user.
type SplitHistoryError struct {
	// Home is the account's folder; Dirs are the split folder names in it.
	Home string
	Dirs []string
}

func (e *SplitHistoryError) Error() string {
	return "The account folder " + e.Home + " has its own " + joinNames(e.Dirs) +
		" instead of sharing this computer's, so conversations started on other accounts cannot be resumed here. " +
		"To fix it: quit anything using this account, move the contents of each of those folders in " + e.Home +
		" into the same-named folder in this computer's Claude folder (the files are uniquely named, nothing is overwritten) " +
		"or delete them if you do not need them, then try again. Removing the account and adding it again also works. " +
		"Tallpine never moves or deletes them for you."
}

// Is matches ErrAgentAccountSplitHistory.
func (e *SplitHistoryError) Is(target error) bool { return target == ErrAgentAccountSplitHistory }

func joinNames(names []string) string {
	out := ""
	for i, name := range names {
		switch {
		case i == 0:
		case i == len(names)-1:
			out += " and "
		default:
			out += ", "
		}
		out += name
	}
	return out
}

// AgentAccountHistoryProbe is an optional capability of a provider: it names
// the shared folders home keeps its own copy of, without changing anything.
type AgentAccountHistoryProbe interface {
	HistorySplit(home string) []string
}

// AgentAccountProviderCLI reads and prepares one provider's CLI config
// folders. home is empty for the computer's own folder. Implementations ask the
// CLI itself; none of them reads a token.
type AgentAccountProviderCLI interface {
	Provider() domain.AgentAccountProvider
	// Identity reports who home is signed in to.
	Identity(ctx context.Context, home string) (domain.AgentAccountIdentityCheck, error)
	// Limits reports the subscription limits of the account signed in to home.
	Limits(ctx context.Context, home string) (domain.AgentAccountLimits, error)
	// LoginArgv is the command that signs home in, run in a terminal with LaunchEnv(home).
	LoginArgv(ctx context.Context) ([]string, error)
	// Logout signs home out through the CLI.
	Logout(ctx context.Context, home string) error
	// Prepare links home to the computer's own folder so a conversation started
	// under one account resumes under another, and the user's skills, MCP servers
	// and settings follow the account. It is safe to run before every launch.
	Prepare(home string) error
	// LaunchEnv is the environment that makes the CLI use home.
	LaunchEnv(home string) map[string]string
}

// AgentAccountStore persists accounts, the default per provider, and which
// account each session uses.
type AgentAccountStore interface {
	ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error)
	CreateAgentAccount(ctx context.Context, account domain.AgentAccount) error
	// UpdateAgentAccountOrg records the organization a saved account's login belongs to.
	UpdateAgentAccountOrg(ctx context.Context, id, orgID, orgName string) error
	// SetAgentAccountLabel names a saved account; ErrAgentAccountUnknown when it is not saved.
	SetAgentAccountLabel(ctx context.Context, id, label string) error
	DeleteAgentAccount(ctx context.Context, id string) error
	GetAgentAccountDefault(ctx context.Context, provider domain.AgentAccountProvider) (string, bool, error)
	SetAgentAccountDefault(ctx context.Context, provider domain.AgentAccountProvider, accountID string) error
	// BindSessionAgentAccount records that the session uses accountID for the
	// provider from now on. Earlier bindings are kept for usage attribution.
	BindSessionAgentAccount(ctx context.Context, sessionID domain.SessionID, provider domain.AgentAccountProvider, accountID string, at time.Time) error
	GetSessionAgentAccount(ctx context.Context, sessionID domain.SessionID, provider domain.AgentAccountProvider) (string, bool, error)
	// AgentAccountUsageSince sums each account's model usage since the time,
	// attributing every usage event to the account its session used then.
	AgentAccountUsageSince(ctx context.Context, since time.Time) ([]domain.AgentAccountUsage, error)
}

// AgentAccountProviderEnv is the account environment of one provider for a
// session, for callers that run a single provider's CLI (reviewers).
type AgentAccountProviderEnv interface {
	// SessionLaunchEnvFor is SessionLaunchEnv limited to provider: another
	// provider's account never affects, or fails, the result. A session bound
	// to the device account gets an empty env.
	SessionLaunchEnvFor(ctx context.Context, sessionID domain.SessionID, provider domain.AgentAccountProvider) (map[string]string, error)
}

// AgentAccountLaunchEnv gives Session Manager the account environment of a
// session's agent launch.
type AgentAccountLaunchEnv interface {
	// SessionLaunchEnv returns the variables that run each provider's CLI under
	// the session's account, binding the provider's default first when the
	// session has none yet.
	SessionLaunchEnv(ctx context.Context, sessionID domain.SessionID) (map[string]string, error)
	// SessionLaunchEnvWith is SessionLaunchEnv with accountID in place of the
	// session's account for its provider. It binds nothing.
	SessionLaunchEnvWith(ctx context.Context, sessionID domain.SessionID, accountID string) (map[string]string, error)
	// BindSession pins the session to accountID, or to the provider's default
	// when accountID is empty.
	BindSession(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, accountID string) error
	// CommitSession records that the session now uses accountID, with no
	// sign-in probe: a move calls it once its agent already runs there.
	CommitSession(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, accountID string) error
	// SessionAccountID is the account the session uses for harness's provider,
	// or "" when the harness has no accounts.
	SessionAccountID(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness) (string, error)
	// ValidateAccount checks that accountID can run harness now: it is of the
	// harness's provider and signed in as the login it was added with.
	ValidateAccount(ctx context.Context, harness domain.AgentHarness, accountID string) error
}

// AccountPool is a provider-neutral view of the accounts a harness can run
// under, so placement and failover do not depend on one provider's CLI.
type AccountPool interface {
	// List returns every account of the provider.
	List(ctx context.Context, provider domain.AgentAccountProvider) ([]domain.AgentAccount, error)
	// Usable returns the provider's accounts that can launch as themselves (a
	// subscription sign-in or an API key) and are not being removed.
	Usable(ctx context.Context, provider domain.AgentAccountProvider) ([]domain.AgentAccount, error)
	// Limits is the account's latest usage limits; ok is false when none were
	// read.
	Limits(ctx context.Context, accountID string) (limits domain.AgentAccountLimits, ok bool, err error)
	// Bind pins the session to accountID, or places it by the placement
	// policy when accountID is empty.
	Bind(ctx context.Context, sessionID domain.SessionID, harness domain.AgentHarness, accountID string) error
}
