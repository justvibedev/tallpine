package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	scmgithub "tallpine.eu/tallpine/backend/internal/adapters/scm/github"
	scmgitlab "tallpine.eu/tallpine/backend/internal/adapters/scm/gitlab"
	scmmulti "tallpine.eu/tallpine/backend/internal/adapters/scm/multi"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	scmobserve "tallpine.eu/tallpine/backend/internal/observe/scm"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// startSCMObserver wires the provider-neutral SCM observer with both GitHub
// and GitLab providers via a multi Provider dispatcher. Missing credentials
// for one provider do not prevent the other from starting; the observer is
// disabled only when no provider has usable credentials.
// The SCM observer polls ended sessions' open PRs through this optional store
// method (see observe/scm endedPRSessionLister); losing it silently reverts to
// the B24 behavior where a merge after the worker was killed is never recorded.
var _ interface {
	ListEndedSessionsWithOpenBacklogPRs(context.Context) ([]domain.SessionRecord, error)
} = (*sqlite.Store)(nil)

func startSCMObserver(ctx context.Context, store *sqlite.Store, lcm *lifecycle.Manager, branchStates scmobserve.BranchStateReconciler, gitlabCfg config.GitLabConfig, logger *slog.Logger) <-chan struct{} {
	var named []scmmulti.NamedProvider

	ghProvider, ghErr := newGitHubSCMProvider(logger)
	if ghErr != nil {
		logSCMProviderDisabled(logger, "github", ghErr)
	} else {
		named = append(named, scmmulti.NamedProvider{Key: "github", Provider: ghProvider})
	}

	glProvider, glErr := newGitLabSCMProvider(gitlabCfg, logger)
	if glErr != nil {
		logSCMProviderDisabled(logger, "gitlab", glErr)
	} else {
		named = append(named, scmmulti.NamedProvider{Key: "gitlab", Provider: glProvider})
	}

	if len(named) == 0 {
		logger.Warn("scm observer disabled: no usable SCM provider")
		return closedDone()
	}
	provider := scmmulti.New(named...)
	if lcm != nil {
		lcm.SetReviewThreadReader(reviewThreadReader{provider: provider})
		go restoreDeferredReviewComments(ctx, store, lcm, logger)
	}
	observer := scmobserve.New(provider, store, lcm, scmobserve.Config{Logger: logger, ScopedIdentityResolver: provider, BranchStates: branchStates, ReviewRuns: store})
	return observer.Start(ctx)
}

// activeGitHubEnterprise is the daemon-wide GHES config the GitHub constructor reads.
var activeGitHubEnterprise atomic.Pointer[config.GitHubEnterpriseConfig]

// installActiveGitHub publishes the daemon-wide GitHub accounts and GHES config
// the SCM constructors read, registers the GHES hosts with the domain host rule,
// and returns the cleanup that clears them.
func installActiveGitHub(accounts *githubAccounts, ent config.GitHubEnterpriseConfig) func() {
	activeGitHubAccounts.Store(accounts)
	activeGitHubEnterprise.Store(&ent)
	domain.SetGitHubEnterpriseHosts(ent.Hosts)
	return func() {
		activeGitHubAccounts.Store(nil)
		activeGitHubEnterprise.Store(nil)
		domain.SetGitHubEnterpriseHosts(nil)
	}
}

func newGitHubSCMProvider(logger *slog.Logger) (*scmgithub.Provider, error) {
	tokens := scmgithub.FallbackTokenSource{
		scmgithub.EnvTokenSource{EnvVars: []string{"TALLPINE_GITHUB_TOKEN"}},
		&scmgithub.GHTokenSource{},
	}
	opts := scmgithub.ProviderOptions{Token: tokens, SkipTokenPreflight: true, Logger: logger}
	if accounts := activeGitHubAccounts.Load(); accounts != nil {
		if _, pinned := explicitGitHubTokenEnvSet(); !pinned {
			opts.Accounts = accounts
		}
	}
	opts.KnownHosts = knownProjectHosts
	if ent := activeGitHubEnterprise.Load(); ent != nil {
		opts.EnterpriseHosts, opts.HostTokens = ent.Hosts, ent.HostTokens
	}
	return scmgithub.NewProvider(opts)
}

func newGitLabSCMProvider(gitlabCfg config.GitLabConfig, logger *slog.Logger) (*scmgitlab.Provider, error) {
	tokens := scmgitlab.FallbackTokenSource{
		scmgitlab.EnvTokenSource{EnvVars: []string{"TALLPINE_GITLAB_TOKEN"}},
		&scmgitlab.GLabTokenSource{},
	}
	hostTokens := make(map[string]scmgitlab.TokenSource, len(gitlabCfg.HostTokens))
	for host, token := range gitlabCfg.HostTokens {
		hostTokens[host] = scmgitlab.StaticTokenSource(token)
	}
	return scmgitlab.NewProvider(scmgitlab.ProviderOptions{
		Token:              tokens,
		SkipTokenPreflight: true,
		Logger:             logger,
		AllowedHosts:       gitlabCfg.AllowedHosts,
		HostTokens:         hostTokens,
	})
}

func logSCMProviderDisabled(logger *slog.Logger, provider string, err error) {
	if errors.Is(err, scmgithub.ErrNoToken) || errors.Is(err, scmgithub.ErrAuthFailed) ||
		errors.Is(err, scmgitlab.ErrNoToken) || errors.Is(err, scmgitlab.ErrAuthFailed) {
		logger.Warn("scm provider disabled: no usable token", "provider", provider, "err", err)
	} else {
		logger.Warn("scm provider disabled: setup failed", "provider", provider, "err", err)
	}
}

// newMultiSCMProvider builds a multi-provider for use outside the polling
// observer (e.g. session service PR claiming). Returns nil when no provider
// has usable credentials — callers must tolerate a nil SCM.
func newMultiSCMProvider(gitlabCfg config.GitLabConfig, logger *slog.Logger) *scmmulti.Provider {
	var named []scmmulti.NamedProvider
	if gh, err := newGitHubSCMProvider(logger); err == nil {
		named = append(named, scmmulti.NamedProvider{Key: "github", Provider: gh})
	}
	if gl, err := newGitLabSCMProvider(gitlabCfg, logger); err == nil {
		named = append(named, scmmulti.NamedProvider{Key: "gitlab", Provider: gl})
	}
	if len(named) == 0 {
		return nil
	}
	return scmmulti.New(named...)
}

// newMultiSCMMerger builds a multi-merger for PR merge actions, registering
// both GitHub and GitLab providers. When one provider is unavailable (missing
// token), the multi-merger still routes to the healthy one — same
// degrade-gracefully pattern as newMultiSCMProvider. Returns nil when no
// provider has usable credentials.
func newMultiSCMMerger(gitlabCfg config.GitLabConfig, logger *slog.Logger) *scmmulti.Merger {
	var named []scmmulti.NamedMerger
	if gh, err := newGitHubSCMProvider(logger); err == nil {
		named = append(named, scmmulti.NamedMerger{Key: "github", Merger: gh})
	}
	if gl, err := newGitLabSCMProvider(gitlabCfg, logger); err == nil {
		named = append(named, scmmulti.NamedMerger{Key: "gitlab", Merger: gl})
	}
	if len(named) == 0 {
		return nil
	}
	return scmmulti.NewMerger(named...)
}

func closedDone() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

// reviewThreadReader lets lifecycle re-read a tracked PR's review threads
// right before it delivers a review comment to the worker.
type reviewThreadReader struct {
	provider interface {
		FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)
	}
}

func (r reviewThreadReader) ReadReviewThreads(ctx context.Context, pr domain.PullRequest) (ports.SCMReviewObservation, error) {
	repo, ok := scmRepoForTrackedPR(pr)
	if !ok || pr.Number <= 0 {
		return ports.SCMReviewObservation{}, fmt.Errorf("pull request %s has no provider repository", pr.URL)
	}
	return r.provider.FetchReviewThreads(ctx, ports.SCMPRRef{Repo: repo, Number: pr.Number, URL: pr.URL})
}

func scmRepoForTrackedPR(pr domain.PullRequest) (ports.SCMRepo, bool) {
	owner, name, ok := strings.Cut(strings.TrimSpace(pr.Repo), "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return ports.SCMRepo{}, false
	}
	provider := strings.ToLower(strings.TrimSpace(pr.Provider))
	host := strings.ToLower(strings.TrimSpace(pr.Host))
	if provider == "" || host == "" {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: provider, Host: host, Owner: owner, Name: name, Repo: pr.Repo}, true
}

// restoreDeferredReviewComments hands lifecycle the live workers once at
// startup so review comments deferred before a restart are delivered again.
func restoreDeferredReviewComments(ctx context.Context, store *sqlite.Store, lcm *lifecycle.Manager, logger *slog.Logger) {
	sessions, err := store.ListAllSessions(ctx)
	if err != nil {
		logger.Warn("restore deferred review comments: list sessions", "err", err)
		return
	}
	ids := make([]domain.SessionID, 0, len(sessions))
	for _, rec := range sessions {
		if rec.Kind == domain.KindWorker && !rec.IsTerminated {
			ids = append(ids, rec.ID)
		}
	}
	lcm.RestoreDeferredReviewComments(ctx, ids)
}

// knownProjectHosts lists the SCM hosts of registered projects' origin remotes.
func knownProjectHosts(ctx context.Context) []string {
	accounts := activeGitHubAccounts.Load()
	if accounts == nil || accounts.projects == nil {
		return nil
	}
	projects, err := accounts.projects.ListProjects(ctx)
	if err != nil {
		return nil
	}
	var hosts []string
	for _, p := range projects {
		if id, err := domain.ParseRepositoryIdentity(p.RepoOriginURL); err == nil {
			hosts = append(hosts, id.Host)
		}
	}
	return hosts
}
