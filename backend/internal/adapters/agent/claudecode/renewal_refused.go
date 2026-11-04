package claudecode

import (
	"context"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/pkg/agentcreds"
)

// An expired stored login reads as configured because Claude Code renews it on
// its next run. When that run then reports the account refused, the renewal
// failed: the refresh token is dead too, and calling the login configured
// would keep readiness ("authorized") at odds with every launch (B520).
// claudeLoginRefusals remembers, per credential fingerprint, when the provider
// refused that expired login, so only that credential reads as signed out;
// other accounts' expired logins may still renew. A new login has a new secret
// (and fingerprint) and is judged afresh.
var claudeLoginRefusals struct {
	mu sync.Mutex
	at map[string]time.Time
}

// NoteProviderRefusedLogin is the runtime 401 handler's full correction for the
// launch context (workingDir, env) the provider refused: it drops the cached
// verdict and, when that launch's credential had already expired, marks it as
// one Claude Code failed to renew. An unresolvable credential only invalidates.
func NoteProviderRefusedLogin(ctx context.Context, binary, workingDir string, env map[string]string) {
	InvalidateAuthCache()
	probeCtx, cancel := context.WithTimeout(ctx, claudeAuthProbeTimeout)
	defer cancel()
	noteLoginRefused((&Plugin{}).resolveProviderContext(probeCtx, binary, workingDir, env, claudeModelAuthReport))
}

// expiredLoginStatus decides a stored login past its expiry without asking the
// provider (it could only answer 401): configured while Claude Code may still
// renew it, unauthorized once the provider refused it after it expired. ok is
// false for a login that has not expired or cannot be renewed.
func expiredLoginStatus(resolved claudeProviderContext) (ports.AgentAuthStatus, string, bool) {
	if !resolved.found || !resolved.credential.ExpiredButRenewable(claudeNow()) {
		return "", "", false
	}
	if renewalRefused(resolved.credential) {
		return ports.AgentAuthStatusUnauthorized, "token_renewal_refused", true
	}
	return ports.AgentAuthStatusConfigured, "token_renewal_pending", true
}

// noteLoginRefused records a refusal of resolved's credential when it had
// already expired; a refusal of an unexpired login says nothing about renewal.
func noteLoginRefused(resolved claudeProviderContext) {
	now := claudeNow()
	if !resolved.found || !resolved.credential.ExpiredButRenewable(now) {
		return
	}
	fingerprint := resolved.credential.Fingerprint()
	if fingerprint == "" {
		return
	}
	claudeLoginRefusals.mu.Lock()
	if claudeLoginRefusals.at == nil {
		claudeLoginRefusals.at = map[string]time.Time{}
	}
	claudeLoginRefusals.at[fingerprint] = now
	claudeLoginRefusals.mu.Unlock()
}

// renewalRefused reports whether cred is an expired login that the provider
// has refused since it expired, i.e. Claude Code already tried to renew it.
func renewalRefused(cred agentcreds.Credential) bool {
	claudeLoginRefusals.mu.Lock()
	at, ok := claudeLoginRefusals.at[cred.Fingerprint()]
	claudeLoginRefusals.mu.Unlock()
	return ok && !cred.ExpiresAt.IsZero() && !at.Before(cred.ExpiresAt)
}
