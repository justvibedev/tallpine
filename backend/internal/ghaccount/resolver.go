package ghaccount

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	defaultPickTTL   = 10 * time.Minute
	defaultLoginsTTL = time.Minute
	defaultTokenTTL  = 5 * time.Minute
	stepTimeout      = 8 * time.Second
)

// Resolver picks the gh account for a repo. The zero value is not usable; use
// NewResolver. Safe for concurrent use.
type Resolver struct {
	GH   GH
	Push PushChecker
	// Identity, when set, verifies a fetched token really is the requested login.
	Identity IdentityChecker
	SSHHost  SSHHostFunc
	Logger   *slog.Logger
	Now      func() time.Time

	// StepTimeout bounds one gh or API step; zero means the default.
	StepTimeout time.Duration
	PickTTL     time.Duration
	LoginsTTL   time.Duration
	TokenTTL    time.Duration

	mu      sync.Mutex
	logins  []string
	loginAt time.Time
	picks   map[string]pick
	flight  singleflight.Group
	tokens  map[string]cachedToken
}

type pick struct {
	login       string
	fingerprint string
	at          time.Time
}

type cachedToken struct {
	token string
	at    time.Time
}

// NewResolver wires the real gh, ssh and GitHub REST implementations.
func NewResolver(logger *slog.Logger) *Resolver {
	return &Resolver{GH: ExecGH{}, Push: RESTPushChecker{}, Identity: RESTIdentityChecker{}, SSHHost: ExecSSHHost, Logger: logger}
}

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func dur(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

func (r *Resolver) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// ResolveRemote returns the login to use for a project's remote, or "" to keep
// the default credentials (no gh, one account, non-GitHub remote, no push
// access anywhere). override is the project's explicit login and wins outright.
func (r *Resolver) ResolveRemote(ctx context.Context, remote, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	repo, ok := ParseRemote(ctx, remote, r.SSHHost)
	if !ok {
		return ""
	}
	return r.ResolveRepo(ctx, repo, "")
}

// ResolveRepo is ResolveRemote for an already-parsed repo.
func (r *Resolver) ResolveRepo(ctx context.Context, repo Repo, override string) string {
	if override = strings.TrimSpace(override); override != "" {
		return override
	}
	logins, err := r.listLogins(ctx)
	if err != nil || len(logins) < 2 {
		return ""
	}
	fp := strings.Join(sortedCopy(logins), ",")
	key := repo.Key()
	r.mu.Lock()
	if p, ok := r.picks[key]; ok && p.fingerprint == fp && r.now().Sub(p.at) < dur(r.PickTTL, defaultPickTTL) {
		r.mu.Unlock()
		return p.login
	}
	r.mu.Unlock()

	// Concurrent resolves of one repo share a single run of the gh and API calls.
	v, _ := r.shared(ctx, "pick:"+key+":"+fp, pickBudget, func(ctx context.Context) (any, error) {
		r.mu.Lock()
		if p, ok := r.picks[key]; ok && p.fingerprint == fp && r.now().Sub(p.at) < dur(r.PickTTL, defaultPickTTL) {
			r.mu.Unlock()
			return p.login, nil
		}
		r.mu.Unlock()
		login, definitive := r.pickLogin(ctx, repo, logins)
		if definitive {
			r.mu.Lock()
			if r.picks == nil {
				r.picks = map[string]pick{}
			}
			r.picks[key] = pick{login: login, fingerprint: fp, at: r.now()}
			r.mu.Unlock()
		}
		return login, nil
	})
	login, _ := v.(string)
	return login
}

// pickLogin checks the owner's own account first, then the others. definitive
// is false when a transient error left the answer unknown, so it is not cached.
func (r *Resolver) pickLogin(ctx context.Context, repo Repo, logins []string) (login string, definitive bool) {
	ordered := make([]string, 0, len(logins))
	for _, l := range logins {
		if strings.EqualFold(l, repo.Owner) {
			ordered = append(ordered, l)
		}
	}
	for _, l := range logins {
		if !strings.EqualFold(l, repo.Owner) {
			ordered = append(ordered, l)
		}
	}
	definitive = true
	for _, l := range ordered {
		tctx, cancel := context.WithTimeout(ctx, dur(r.StepTimeout, stepTimeout))
		token, err := r.Token(tctx, l)
		if err != nil {
			if tctx.Err() != nil {
				definitive = false // timed out: unknown, so do not cache "no push"
			}
			cancel()
			continue
		}
		can, err := r.Push.CanPush(tctx, token, repo)
		cancel()
		if err != nil {
			definitive = false
			r.logger().Debug("github account permission check failed", "repo", repo.Key(), "account", l, "err", err)
			continue
		}
		if can {
			return l, true
		}
	}
	return "", definitive
}

func (r *Resolver) listLogins(ctx context.Context) ([]string, error) {
	r.mu.Lock()
	if r.logins != nil && r.now().Sub(r.loginAt) < dur(r.LoginsTTL, defaultLoginsTTL) {
		defer r.mu.Unlock()
		return r.logins, nil
	}
	r.mu.Unlock()
	v, err := r.shared(ctx, "logins", stepTimeout, func(ctx context.Context) (any, error) {
		r.mu.Lock()
		if r.logins != nil && r.now().Sub(r.loginAt) < dur(r.LoginsTTL, defaultLoginsTTL) {
			defer r.mu.Unlock()
			return r.logins, nil // a flight that just finished already refreshed it
		}
		r.mu.Unlock()
		tctx, cancel := context.WithTimeout(ctx, stepTimeout)
		defer cancel()
		logins, err := r.GH.Logins(tctx)
		if err != nil {
			return nil, err
		}
		if logins == nil {
			logins = []string{}
		}
		r.mu.Lock()
		r.logins, r.loginAt = logins, r.now()
		r.mu.Unlock()
		return logins, nil
	})
	if err != nil {
		return nil, err
	}
	logins, _ := v.([]string)
	return logins, nil
}

// Token returns the account's token, memoized in memory only. It is never
// logged or written anywhere.
func (r *Resolver) Token(ctx context.Context, login string) (string, error) {
	if !domain.IsGitHubLogin(login) {
		return "", ErrInvalidLogin
	}
	r.mu.Lock()
	if c, ok := r.tokens[login]; ok && r.now().Sub(c.at) < dur(r.TokenTTL, defaultTokenTTL) {
		r.mu.Unlock()
		return c.token, nil
	}
	r.mu.Unlock()
	v, err := r.shared(ctx, "token:"+login, stepTimeout, func(ctx context.Context) (any, error) {
		r.mu.Lock()
		if c, ok := r.tokens[login]; ok && r.now().Sub(c.at) < dur(r.TokenTTL, defaultTokenTTL) {
			r.mu.Unlock()
			return c.token, nil
		}
		r.mu.Unlock()
		token, err := r.GH.Token(ctx, login)
		if err != nil {
			return "", err
		}
		if token == "" {
			return "", ErrNoToken
		}
		if r.Identity != nil {
			actual, err := r.Identity.Login(ctx, token)
			if err == nil && !strings.EqualFold(actual, login) {
				r.logger().Warn("github account token belongs to another account; refusing it", "wanted", login, "actual", actual)
				return "", ErrWrongAccount
			}
			// A failed check (offline) is not proof of a mismatch: keep the token.
		}
		r.mu.Lock()
		if r.tokens == nil {
			r.tokens = map[string]cachedToken{}
		}
		r.tokens[login] = cachedToken{token: token, at: r.now()}
		r.mu.Unlock()
		return token, nil
	})
	if err != nil {
		return "", err
	}
	token, _ := v.(string)
	return token, nil
}

// InvalidateToken forgets the memoized token of one login (auth failure).
func (r *Resolver) InvalidateToken(login string) {
	r.mu.Lock()
	delete(r.tokens, login)
	r.mu.Unlock()
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
