// Package scm implements the provider-neutral SCM polling observer. It owns the
// polling loop, ETag/cache checks, semantic diffing, DB persistence, and
// lifecycle notification; provider adapters only normalize provider-specific
// APIs into ports.SCMObservation values.
package scm

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/observe"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Observer coordinates provider polling, semantic diffing, persistence, and
// lifecycle notifications for SCM observations.
type Observer struct {
	// provider is the SCM adapter used for all provider/network operations.
	provider Provider
	// store supplies sessions/projects/local PR state and receives transactional writes.
	store Store
	// lifecycle is notified after successful persistence of meaningful changes.
	lifecycle Lifecycle
	// tick is the active PR/CI polling cadence.
	tick time.Duration
	// reviewInterval is the minimum duration between review-thread fetches per PR.
	reviewInterval time.Duration
	// clock supplies observation timestamps.
	clock func() time.Time
	// logger receives non-fatal operational failures.
	logger *slog.Logger
	// credentialsChecked records whether an optional provider credential gate ran.
	credentialsChecked bool
	// disabled is set while the credential gate reports unavailable credentials.
	disabled bool
	// scopedIdentityResolver resolves the authenticated identity per provider key.
	scopedIdentityResolver ports.ScopedIdentityResolver
	// rateLimitUntil records, per provider key, the time until which that
	// provider's calls should be skipped. Set when a provider returns a
	// rate-limit error so the observer applies a cooldown instead of polling
	// every 30s
	rateLimitUntil map[string]time.Time
	// Cache holds bounded in-memory provider ETags and review poll timestamps.
	Cache ObserverCache
	// branchStates refreshes local branch facts on each tick.
	branchStates BranchStateReconciler
	// reviewRuns reads Tallpine review passes for the no-CI merge gate.
	reviewRuns ReviewRunReader
	// noCIRecheck records, per tracked PR, the stored state a no-CI gate
	// refetch was last forced for, so a mismatch the provider does not
	// resolve triggers one refetch rather than one per tick.
	noCIRecheck map[string]string
	// guardFailLogged records, per repo, the last guard error text logged at
	// error level, so a persistent failure (a repo the token cannot read) is
	// reported once rather than on every tick.
	guardFailLogged map[string]string
}

// New constructs an Observer with default cadence/cache settings for zero
// values in cfg.
func New(provider Provider, store Store, lifecycle Lifecycle, cfg Config) *Observer {
	o := &Observer{provider: provider, store: store, lifecycle: lifecycle, tick: cfg.Tick, reviewInterval: cfg.ReviewInterval, clock: cfg.Clock, logger: cfg.Logger, scopedIdentityResolver: cfg.ScopedIdentityResolver, Cache: newCache(cfg.CacheMax), rateLimitUntil: map[string]time.Time{}, branchStates: cfg.BranchStates, reviewRuns: cfg.ReviewRuns, noCIRecheck: map[string]string{}, guardFailLogged: map[string]string{}}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.reviewInterval <= 0 {
		o.reviewInterval = DefaultReviewInterval
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start launches the observer loop. The first Poll runs immediately inside the
// goroutine so daemon startup is not blocked; subsequent polls run on the tick.
//
// The first invocation of poll inside the supervisor also runs checkCredentials
// up front. That way the "scm observer disabled: provider credentials
// unavailable" warning is emitted on a fresh daemon even if discoverSubjects
// has no subjects yet (which would otherwise short-circuit Poll before
// checkCredentials). The wrapper runs once per process; a failed credential
// verdict is retried by Poll when subjects become available, while a successful
// verdict is cached.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	var credentialGate sync.Once
	poll := func(ctx context.Context) error {
		credentialGate.Do(func() {
			if _, err := o.checkCredentials(ctx); err != nil && !errors.Is(err, context.Canceled) {
				o.logger.Error("scm observer: initial credential check failed", "err", err)
			}
		})
		return o.Poll(ctx)
	}
	return observe.StartPollLoop(ctx, o.tick, poll, o.logger, "scm observer")
}

// Poll runs one synchronous SCM observation cycle.
func (o *Observer) Poll(ctx context.Context) (pollErr error) {
	pollStarted := time.Now()
	o.logger.Debug("scm observer: poll started", "started_at", pollStarted.UTC())
	defer func() {
		completedAt := time.Now()
		duration := completedAt.Sub(pollStarted)
		attrs := []any{
			"stage", "poll",
			"started_at", pollStarted.UTC(),
			"completed_at", completedAt.UTC(),
			"duration_ms", duration.Milliseconds(),
		}
		if pollErr != nil {
			attrs = append(attrs, "err", pollErr)
		}
		if duration >= o.tick {
			o.logger.Warn("scm observer: slow poll completed", attrs...)
			return
		}
		o.logger.Debug("scm observer: poll completed", attrs...)
	}()
	now := o.clock().UTC()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Branch facts are local git reads, so they run before, and never depend
	// on, the provider credential gate below.
	if o.branchStates != nil {
		if err := o.branchStates.ReconcileBranchStates(ctx); err != nil && !errors.Is(err, context.Canceled) {
			o.logger.Warn("scm observer: branch state reconcile failed", "err", err)
		}
	}
	subjects, sessionRepos, err := o.discoverSubjects(ctx)
	if err != nil {
		return err
	}
	if len(sessionRepos) == 0 {
		return nil
	}
	proceed, err := o.checkCredentials(ctx)
	if err != nil {
		return err
	}
	if !proceed || o.disabled {
		return nil
	}

	repoGuards := o.guardRepos(ctx, sessionRepos)
	tracker := newRepoRefreshTracker(repoGuards)
	repoRefreshOK := tracker.ok
	markRepoRefreshFailed, markRepoListFailed, markRepoRefreshOK := tracker.markFailed, tracker.markListFailed, tracker.markOK
	if err := ctx.Err(); err != nil {
		return err
	}
	listedPRs, listedRepos := o.discoverNewPRs(ctx, sessionRepos, subjects, repoGuards, now, markRepoListFailed)
	if err := ctx.Err(); err != nil {
		return err
	}

	selection := o.selectRefreshCandidates(ctx, subjects, repoGuards, listedPRs, markRepoRefreshFailed, now)
	// Item 2 — terminal-state reconciliation for GitHub: tracked open PRs
	// not in the current state=open listing may have transitioned to
	// merged/closed. Run a reconciliation pass that issues a full detail
	// fetch per reconciled PR so terminal transitions are not permanently
	// missed. The pass runs only when the repo-list guard is not a 304,
	// and only for GitHub (asymmetry — see reconcileTerminalGitHubPRs).
	// Terminal observations are routed through the normal persistence path;
	// "still open" results are no-op persists that mark the repo
	// refresh-incomplete so the ETag/cursor do not advance.
	reconciledObs := o.reconcileTerminalGitHubPRs(ctx, subjects, repoGuards, listedPRs, &selection, now, markRepoRefreshFailed)
	observations := map[string]ports.SCMObservation{}
	for key, obs := range reconciledObs {
		observations[key] = obs
	}
	prRefreshOK := map[string]bool{}
	for key := range selection.candidateKeys {
		prRefreshOK[key] = false
	}
	if err := o.fetchObservationBatches(ctx, selection, now, observations, markRepoRefreshFailed); err != nil {
		return err
	}

	if err := o.applyNoCIPolicies(ctx, selection, observations); err != nil {
		return err
	}

	reviewModes := map[string]ports.ReviewWriteMode{}
	localOnlyObservations := map[string]bool{}
	reviewStale := map[string]bool{}
	o.refreshReviews(ctx, subjects, observations, selection.subjectsByPR, reviewModes, localOnlyObservations, reviewStale, now)
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := o.persistObservations(ctx, selection, observations, reviewModes, localOnlyObservations, reviewStale, prRefreshOK, now, markRepoRefreshFailed, markRepoRefreshOK); err != nil {
		return err
	}
	o.commitPollProgress(selection, prRefreshOK, reviewModes, repoRefreshOK, repoGuards, listedRepos, now)
	return nil
}
