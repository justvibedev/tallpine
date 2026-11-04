// Package githubcred issues GitHub credentials to agents that run under the
// helper mode of the githubCreds policy rule: per request, per session, with an
// audit line and no token ever written to the agent's environment. The git
// credential helper and the gh shim (`tallpine git-credential`, `tallpine gh-exec`) call it
// through the daemon API with the session's scoped token.
package githubcred

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/githubcreds"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Refusals. Each maps to a distinct API error so the helper can say why.
var (
	// ErrSessionNotFound: no such session.
	ErrSessionNotFound = errors.New("githubcred: session not found")
	// ErrDenied: the policy mode is none; every request is refused.
	ErrDenied = errors.New("githubcred: GitHub credentials are disabled by the agent policy")
	// ErrHostNotAllowed: the host is neither github.com nor the project's remote host.
	ErrHostNotAllowed = errors.New("githubcred: host is not one of this project's GitHub hosts")
	// ErrUnavailable: there is no token for this project (no account, no explicit token).
	ErrUnavailable = errors.New("githubcred: no GitHub token is available for this project")
)

// Sources of an issued token, recorded in the audit line and returned to the
// caller; never the token itself.
const (
	SourceProjectEnv = "project_env"
	SourceAccount    = "account"
	SourceDaemonEnv  = "daemon_env"
)

// Credential is what git needs.
type Credential struct {
	Username string
	Token    string
	Source   string
}

// Decider decides the agent policy for a session; the policy engine satisfies it.
type Decider interface {
	Decide(ctx context.Context, session agentpolicy.SessionRef) agentpolicy.Decision
}

// Sessions looks sessions up.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Projects looks projects up.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Accounts resolves the project's gh account token ("" when it has none).
type Accounts interface {
	TokenForProject(ctx context.Context, remote, override string) (string, error)
}

// Service issues credentials.
type Service struct {
	policy   Decider
	sessions Sessions
	projects Projects
	accounts Accounts
	log      *slog.Logger
	getenv   func(string) string
}

// New builds the service. accounts may be nil (no per-project accounts).
func New(policy Decider, sessions Sessions, projects Projects, accounts Accounts, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{policy: policy, sessions: sessions, projects: projects, accounts: accounts, log: log, getenv: os.Getenv}
}

// explicitTokenVariables are checked in order in the project's environment, then
// the daemon's: an explicit token is the user's own choice and wins over the
// automatic account pick, as it does in env mode.
var explicitTokenVariables = []string{"GH_TOKEN", "GITHUB_TOKEN", "TALLPINE_GITHUB_TOKEN"}

// Issue returns the credential for session on host. The policy is decided now,
// not at launch, so a managed push that disables credentials applies to the
// next request of a running session.
func (s *Service) Issue(ctx context.Context, id domain.SessionID, host string) (Credential, error) {
	rec, ok, err := s.sessions.GetSession(ctx, id)
	if err != nil {
		return Credential{}, fmt.Errorf("load session: %w", err)
	}
	if !ok {
		return Credential{}, ErrSessionNotFound
	}
	// Only mode none refuses. Mode env is served too: such a session is entitled
	// to the token anyway, and one launched under helper (token withheld, shim on
	// PATH) keeps working after Security mode or githubCreds is turned off while
	// it runs, instead of being left with no GitHub access at all.
	mode := githubcreds.Of(s.policy.Decide(ctx, agentpolicy.SessionRef{ID: rec.ID, Project: rec.ProjectID, Kind: rec.Kind, Harness: rec.Harness}))
	if mode.Denied() {
		s.log.Warn("github credential refused: disabled by policy", "session", id, "project", rec.ProjectID, "host", host)
		return Credential{}, ErrDenied
	}
	project, found, err := s.projects.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return Credential{}, fmt.Errorf("load project: %w", err)
	}
	if !found {
		return Credential{}, ErrUnavailable
	}
	if !hostAllowed(host, project.RepoOriginURL) {
		s.log.Warn("github credential refused: host not allowed", "session", id, "project", rec.ProjectID, "host", host)
		return Credential{}, ErrHostNotAllowed
	}
	cred, err := s.token(ctx, project)
	if err != nil {
		s.log.Warn("github credential unavailable", "session", id, "project", rec.ProjectID, "host", host, "error", err)
		return Credential{}, err
	}
	s.log.Info("github credential issued", "session", id, "project", rec.ProjectID, "host", host, "source", cred.Source)
	return cred, nil
}

func hostAllowed(host, remote string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	return host == "github.com" || host == githubcreds.HostOf(remote)
}

// token resolves the credential in env mode's order: a token the project's own
// environment sets, then one the daemon's environment pins explicitly, then the
// project's gh account. A narrow token pinned on the daemon therefore keeps
// scoping the agent when the stricter helper mode is turned on, instead of being
// replaced by the broader account token.
func (s *Service) token(ctx context.Context, project domain.ProjectRecord) (Credential, error) {
	for _, name := range explicitTokenVariables {
		if v := strings.TrimSpace(project.Config.Env[name]); v != "" {
			return Credential{Username: "x-access-token", Token: v, Source: SourceProjectEnv}, nil
		}
	}
	// Ambient GH_TOKEN/GITHUB_TOKEN in the daemon are not a deliberate pin.
	for _, name := range []string{"TALLPINE_GITHUB_TOKEN"} {
		if v := strings.TrimSpace(s.getenv(name)); v != "" {
			return Credential{Username: "x-access-token", Token: v, Source: SourceDaemonEnv}, nil
		}
	}
	if s.accounts != nil {
		token, err := s.accounts.TokenForProject(ctx, project.RepoOriginURL, project.Config.GitHubUser)
		if err != nil {
			return Credential{}, ErrUnavailable
		}
		if token != "" {
			return Credential{Username: "x-access-token", Token: token, Source: SourceAccount}, nil
		}
	}
	return Credential{}, ErrUnavailable
}
