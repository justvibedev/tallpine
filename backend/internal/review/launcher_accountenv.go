package review

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/pricing"
)

const claudeConfigDirKey = "CLAUDE_CONFIG_DIR"

// AccountEnvSlot is an ports.AgentAccountProviderEnv bound after the launcher
// is built: the accounts service is constructed later than the review engine,
// and the engine is needed to build the accounts service, so neither can be
// passed to the other's constructor. This is a deliberate late-binding slot:
// the daemon creates it empty, hands it to the launcher via WithAgentAccounts
// and calls Set once the accounts service exists. Until Set is called it
// yields no account env (reviewers then run under the daemon's login).
type AccountEnvSlot struct {
	mu  sync.RWMutex
	src ports.AgentAccountProviderEnv
}

// Set binds the source.
func (s *AccountEnvSlot) Set(src ports.AgentAccountProviderEnv) {
	s.mu.Lock()
	s.src = src
	s.mu.Unlock()
}

// SessionLaunchEnvFor implements ports.AgentAccountProviderEnv.
func (s *AccountEnvSlot) SessionLaunchEnvFor(ctx context.Context, id domain.SessionID, provider domain.AgentAccountProvider) (map[string]string, error) {
	s.mu.RLock()
	src := s.src
	s.mu.RUnlock()
	if src == nil {
		return nil, nil
	}
	return src.SessionLaunchEnvFor(ctx, id, provider)
}

// WithAgentAccounts runs Claude reviewers under the account their worker is
// bound to, instead of the daemon's login.
func WithAgentAccounts(src ports.AgentAccountProviderEnv) LauncherOption {
	return func(l *agentLauncher) { l.accounts = src }
}

// withAccountEnv returns spec with the worker's Claude account variables laid
// over its project env (the account wins). Every later use of spec.ProjectEnv
// (restore probes, the process env, chat start) then sees the account.
// Other harnesses and device-bound workers are returned unchanged.
func (l *agentLauncher) withAccountEnv(ctx context.Context, spec LaunchSpec) (LaunchSpec, error) {
	if l.accounts == nil || spec.Harness != domain.ReviewerClaudeCode {
		return spec, nil
	}
	account, err := l.claudeAccountEnv(ctx, spec.WorkerID)
	if err != nil {
		return spec, fmt.Errorf("%w (Claude account %s)", err, l.accountLabel(ctx, spec.WorkerID))
	}
	slog.Info("reviewer launch account", "worker", spec.WorkerID, "review", spec.ReviewSessionID, "account", l.accountLabel(ctx, spec.WorkerID), "config_dir", account[claudeConfigDirKey])
	if len(account) == 0 {
		return spec, nil
	}
	merged := make(map[string]string, len(spec.ProjectEnv)+len(account))
	for k, v := range spec.ProjectEnv {
		merged[k] = v
	}
	for k, v := range account {
		merged[k] = v
	}
	spec.ProjectEnv = merged
	return spec, nil
}

// claudeAccountEnv is the Claude variables of the worker's account: nothing for
// a device-bound worker. Only Claude's own lookup is asked, so another
// provider's account can never fail a Claude reviewer. A failure that still
// leaves a usable folder (the account's shared history could not be linked) is
// logged and tolerated, as the worker launch does.
func (l *agentLauncher) claudeAccountEnv(ctx context.Context, worker domain.SessionID) (map[string]string, error) {
	if l.accounts == nil {
		return nil, nil
	}
	all, err := l.accounts.SessionLaunchEnvFor(ctx, worker, domain.AgentAccountProviderClaude)
	if err != nil && all[claudeConfigDirKey] == "" {
		return nil, fmt.Errorf("resolve agent account for reviewer of %s: %w", worker, err)
	}
	if err != nil {
		slog.Warn("reviewer agent account env incomplete; continuing with Claude account", "worker", worker, "err", err)
	}
	var out map[string]string
	for _, key := range pricing.ClaudeLaunchEnvKeys() {
		if value, ok := all[key]; ok {
			if out == nil {
				out = map[string]string{}
			}
			out[key] = value
		}
	}
	return out, nil
}

// accountIdentity is the optional lookup of the account a session runs on.
type accountIdentity interface {
	SessionAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, bool, error)
}

// SessionAccount forwards to the bound source when it can name accounts.
func (s *AccountEnvSlot) SessionAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, bool, error) {
	s.mu.RLock()
	src := s.src
	s.mu.RUnlock()
	if ai, ok := src.(accountIdentity); ok {
		return ai.SessionAccount(ctx, id, p)
	}
	return domain.AgentAccount{}, false, nil
}

// accountLabel names the worker's Claude account for logs and errors: the
// email, else the id, else "unknown".
func (l *agentLauncher) accountLabel(ctx context.Context, worker domain.SessionID) string {
	if ai, ok := l.accounts.(accountIdentity); ok {
		if a, _, err := ai.SessionAccount(ctx, worker, domain.AgentAccountProviderClaude); err == nil {
			if a.Email != "" {
				return a.Email
			}
			if a.ID != "" {
				return a.ID
			}
		}
	}
	return "unknown"
}

// recordLaunch remembers the config dir a reviewer handle started under and
// names the account in a launch failure.
func (l *agentLauncher) recordLaunch(ctx context.Context, spec LaunchSpec, res LaunchResult, err error) (LaunchResult, error) {
	if err != nil {
		if l.accounts != nil && spec.Harness == domain.ReviewerClaudeCode {
			err = fmt.Errorf("%w (Claude account %s)", err, l.accountLabel(ctx, spec.WorkerID))
		}
		return res, err
	}
	if res.HandleID != "" {
		l.launchedHome.Store(res.HandleID, spec.ProjectEnv[claudeConfigDirKey])
	}
	return res, nil
}

// AccountStale reports whether a live reviewer handle runs under a Claude
// account other than its worker's current one. A handle with no record (the
// daemon restarted) counts as stale: relaunching resumes its conversation.
func (l *agentLauncher) AccountStale(ctx context.Context, handleID string, worker domain.SessionID, harness domain.ReviewerHarness) bool {
	if l.accounts == nil || harness != domain.ReviewerClaudeCode {
		return false
	}
	cur, err := l.claudeAccountEnv(ctx, worker)
	if err != nil {
		return false
	}
	prev, ok := l.launchedHome.Load(handleID)
	if !ok {
		return true
	}
	stale := prev != cur[claudeConfigDirKey]
	if stale {
		slog.Info("reviewer account changed; relaunching", "worker", worker, "handle", handleID, "account", l.accountLabel(ctx, worker))
	}
	return stale
}
