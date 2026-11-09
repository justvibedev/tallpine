package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts/codex"
)

const (
	defaultDisplayReadinessTTL = 5 * time.Minute
	defaultLaunchReadinessTTL  = 30 * time.Second
	defaultInstallCheckTimeout = 2 * time.Second
	defaultAuthCheckTimeout    = 10 * time.Second
	defaultReadinessWorkers    = 4
)

type readinessInvalidation uint8

const (
	readinessInvalidateInstallation readinessInvalidation = 1 << iota
	readinessInvalidateAuthentication
	readinessInvalidateAll = readinessInvalidateInstallation | readinessInvalidateAuthentication
)

type readinessCoordinatorConfig struct {
	Agents              []ports.HarnessAgent
	Factory             func() []ports.HarnessAgent
	Context             context.Context
	Logger              *slog.Logger
	Now                 func() time.Time
	DisplayTTL          time.Duration
	LaunchTTL           time.Duration
	InstallTimeout      time.Duration
	AuthTimeout         time.Duration
	RetryDelays         []time.Duration
	Workers             int
	AuthenticationCheck func(context.Context, string, domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool)
	// AuthenticationEnv returns the launch environment an agent's auth check
	// should run under instead of the daemon's own, or nil.
	AuthenticationEnv func(ctx context.Context, agentID string) map[string]string
	// OnAuthenticationRecovered runs (asynchronously) when a check observes an
	// agent that was signed out become signed in or configured, however the
	// login happened: Tallpine's login terminal, the agent's own CLI, or a renewed
	// token. Caches derived from the signed-out state hook in here.
	OnAuthenticationRecovered func(agentID string)
}

type readinessEntry struct {
	snapshot        domain.AgentReadinessSnapshot
	invalidated     readinessInvalidation
	checking        readinessInvalidation
	installVersion  uint64
	authVersion     uint64
	failures        int
	nextRetryAt     time.Time
	retryGeneration uint64
}

type readinessCall struct {
	key            readinessCallKey
	done           chan struct{}
	checks         readinessInvalidation
	installVersion uint64
	authVersion    uint64
}

type readinessCallKey struct {
	id           string
	presenceOnly bool
}

type readinessCoordinator struct {
	ctx                 context.Context
	factory             func() []ports.HarnessAgent
	logger              *slog.Logger
	now                 func() time.Time
	displayTTL          time.Duration
	launchTTL           time.Duration
	installTimeout      time.Duration
	authTimeout         time.Duration
	retryDelays         []time.Duration
	workers             int
	authenticationCheck func(context.Context, string, domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool)
	authenticationEnv   func(context.Context, string) map[string]string
	onAuthRecovered     func(agentID string)

	mu      sync.Mutex
	entries map[string]*readinessEntry
	calls   map[readinessCallKey]*readinessCall
}

type unsupportedAgentError struct{ id string }

func (e unsupportedAgentError) Error() string { return fmt.Sprintf("unsupported agent %q", e.id) }

func newReadinessCoordinator(cfg readinessCoordinatorConfig) *readinessCoordinator {
	if cfg.Context == nil {
		cfg.Context = context.Background()
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.DisplayTTL <= 0 {
		cfg.DisplayTTL = defaultDisplayReadinessTTL
	}
	if cfg.LaunchTTL <= 0 {
		cfg.LaunchTTL = defaultLaunchReadinessTTL
	}
	if cfg.InstallTimeout <= 0 {
		cfg.InstallTimeout = defaultInstallCheckTimeout
	}
	if cfg.AuthTimeout <= 0 {
		cfg.AuthTimeout = defaultAuthCheckTimeout
	}
	if len(cfg.RetryDelays) == 0 {
		cfg.RetryDelays = append([]time.Duration(nil), codex.DefaultReadinessRetryDelays...)
	} else {
		cfg.RetryDelays = append([]time.Duration(nil), cfg.RetryDelays...)
	}
	if cfg.Workers <= 0 {
		cfg.Workers = defaultReadinessWorkers
	}
	if cfg.Factory == nil {
		stable := append([]ports.HarnessAgent(nil), cfg.Agents...)
		cfg.Factory = func() []ports.HarnessAgent {
			return append([]ports.HarnessAgent(nil), stable...)
		}
	}
	c := &readinessCoordinator{
		ctx: cfg.Context, factory: cfg.Factory, logger: cfg.Logger, now: cfg.Now,
		displayTTL: cfg.DisplayTTL, launchTTL: cfg.LaunchTTL,
		installTimeout: cfg.InstallTimeout, authTimeout: cfg.AuthTimeout,
		retryDelays: cfg.RetryDelays, workers: cfg.Workers,
		authenticationCheck: cfg.AuthenticationCheck,
		authenticationEnv:   cfg.AuthenticationEnv,
		onAuthRecovered:     cfg.OnAuthenticationRecovered,
		entries:             make(map[string]*readinessEntry, len(cfg.Agents)), calls: make(map[readinessCallKey]*readinessCall),
	}
	for _, item := range cfg.Agents {
		id := string(item.Harness)
		label := item.Name
		if label == "" {
			label = id
		}
		c.entries[id] = &readinessEntry{snapshot: domain.AgentReadinessSnapshot{
			ID: id, Label: label,
			Installation: domain.AgentInstallationObservation{
				State: domain.AgentInstallationUnknown, Freshness: domain.AgentReadinessStale,
				ReasonCode: domain.AgentReadinessReasonNotChecked, Reason: "Installation has not been checked yet.",
			},
			Authentication: domain.AgentAuthenticationObservation{
				State: domain.AgentAuthenticationUnknown, Freshness: domain.AgentReadinessStale,
				ReasonCode: domain.AgentReadinessReasonNotChecked, Reason: "Authentication has not been checked yet.",
			},
		}}
	}
	return c
}

func (c *readinessCoordinator) Snapshot() []domain.AgentReadinessSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := c.sortedIDsLocked()
	out := make([]domain.AgentReadinessSnapshot, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.snapshotLocked(c.entries[id], domain.AgentReadinessPurposeDisplay))
	}
	return out
}

func (c *readinessCoordinator) Ensure(ctx context.Context, agentIDs []string, purpose domain.AgentReadinessPurpose) ([]domain.AgentReadinessSnapshot, error) {
	return c.ensure(ctx, agentIDs, purpose, readinessInvalidateAll)
}

func (c *readinessCoordinator) EnsureInstallation(ctx context.Context, agentIDs []string, purpose domain.AgentReadinessPurpose) ([]domain.AgentReadinessSnapshot, error) {
	return c.ensure(ctx, agentIDs, purpose, readinessInvalidateInstallation)
}

// Force invalidates the requested observations before ensuring them. It is
// reserved for explicit refresh actions; ordinary callers should use Ensure so
// they benefit from the coordinator's freshness policy.
func (c *readinessCoordinator) Force(ctx context.Context, agentIDs []string, purpose domain.AgentReadinessPurpose) ([]domain.AgentReadinessSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !purpose.Valid() {
		return nil, fmt.Errorf("invalid readiness purpose %q", purpose)
	}
	ids, err := c.normalizeIDs(agentIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		c.Invalidate(id, readinessInvalidateAll)
	}
	return c.ensure(ctx, ids, purpose, readinessInvalidateAll)
}

// FindInstalled returns as soon as one bounded installation-only ensure
// confirms a harness. Compatible checks already in progress remain shared and
// continue under the daemon context after this caller stops waiting; presence
// checks never inherit an execution-enabled identity probe.
func (c *readinessCoordinator) FindInstalled(ctx context.Context, purpose domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, bool) {
	if err := ctx.Err(); err != nil || !purpose.Valid() {
		return domain.AgentReadinessSnapshot{}, false
	}
	ids, err := c.normalizeIDs(nil)
	if err != nil || len(ids) == 0 {
		return domain.AgentReadinessSnapshot{}, false
	}
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	matches := make(chan domain.AgentReadinessSnapshot, 1)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(len(ids))
	for _, id := range ids {
		go func(id string) {
			defer wg.Done()
			snapshot, ensureErr := c.ensureOne(waitCtx, id, purpose, readinessInvalidateInstallation, true)
			if ensureErr != nil || snapshot.Installation.State != domain.AgentInstallationInstalled {
				return
			}
			select {
			case matches <- snapshot:
				cancel()
			case <-waitCtx.Done():
			}
		}(id)
	}
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case snapshot := <-matches:
		return snapshot, true
	case <-done:
		select {
		case snapshot := <-matches:
			return snapshot, true
		default:
			return domain.AgentReadinessSnapshot{}, false
		}
	case <-ctx.Done():
		return domain.AgentReadinessSnapshot{}, false
	}
}

func (c *readinessCoordinator) ensure(ctx context.Context, agentIDs []string, purpose domain.AgentReadinessPurpose, requested readinessInvalidation) ([]domain.AgentReadinessSnapshot, error) {
	return c.ensureMode(ctx, agentIDs, purpose, requested, false)
}

func (c *readinessCoordinator) ensureMode(ctx context.Context, agentIDs []string, purpose domain.AgentReadinessPurpose, requested readinessInvalidation, presenceOnly bool) ([]domain.AgentReadinessSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !purpose.Valid() {
		return nil, fmt.Errorf("invalid readiness purpose %q", purpose)
	}
	ids, err := c.normalizeIDs(agentIDs)
	if err != nil {
		return nil, err
	}
	type result struct {
		index int
		item  domain.AgentReadinessSnapshot
		err   error
	}
	results := make(chan result, len(ids))
	semaphore := make(chan struct{}, c.workers)
	for index, id := range ids {
		go func(index int, id string) {
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- result{index: index, err: ctx.Err()}
				return
			}
			item, err := c.ensureOne(ctx, id, purpose, requested, presenceOnly)
			results <- result{index: index, item: item, err: err}
		}(index, id)
	}
	out := make([]domain.AgentReadinessSnapshot, len(ids))
	for range ids {
		res := <-results
		if res.err != nil {
			return nil, res.err
		}
		out[res.index] = res.item
	}
	return out, nil
}

func (c *readinessCoordinator) ensureOne(ctx context.Context, id string, purpose domain.AgentReadinessPurpose, requested readinessInvalidation, presenceOnly bool) (domain.AgentReadinessSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.AgentReadinessSnapshot{}, err
	}
	c.mu.Lock()
	entry := c.entries[id]
	if entry == nil {
		c.mu.Unlock()
		return domain.AgentReadinessSnapshot{}, unsupportedAgentError{id: id}
	}
	needed := c.neededChecksLocked(entry, purpose) & requested
	if needed == 0 {
		snapshot := c.snapshotLocked(entry, purpose)
		c.mu.Unlock()
		c.logDecision(id, purpose, "cache_hit", 0, snapshot, "", time.Time{})
		return snapshot, nil
	}
	if purpose == domain.AgentReadinessPurposeDisplay && !entry.nextRetryAt.IsZero() && c.now().Before(entry.nextRetryAt) {
		snapshot := c.snapshotLocked(entry, purpose)
		nextRetry := entry.nextRetryAt
		c.mu.Unlock()
		c.logDecision(id, purpose, "retry_delayed", 0, snapshot, readinessFailureCategory(snapshot), nextRetry)
		return snapshot, nil
	}
	key := readinessCallKey{id: id, presenceOnly: presenceOnly}
	if call := c.calls[key]; call != nil {
		joinedAt := c.now()
		joinedChecks := call.checks
		joinedInstallVersion := call.installVersion
		joinedAuthVersion := call.authVersion
		wantedInstallVersion := entry.installVersion
		wantedAuthVersion := entry.authVersion
		c.mu.Unlock()
		select {
		case <-call.done:
			c.mu.Lock()
			entry = c.entries[id]
			snapshot := c.snapshotLocked(entry, purpose)
			nextRetry := entry.nextRetryAt
			remaining := c.neededChecksLocked(entry, purpose) & requested
			c.mu.Unlock()
			c.logDecision(id, purpose, "join", c.now().Sub(joinedAt), snapshot, readinessFailureCategory(snapshot), nextRetry)
			installMissed := remaining&readinessInvalidateInstallation != 0 && (joinedChecks&readinessInvalidateInstallation == 0 || joinedInstallVersion < wantedInstallVersion)
			authMissed := remaining&readinessInvalidateAuthentication != 0 && (joinedChecks&readinessInvalidateAuthentication == 0 || joinedAuthVersion < wantedAuthVersion)
			if installMissed || authMissed {
				return c.ensureOne(ctx, id, purpose, requested, presenceOnly)
			}
			return snapshot, nil
		case <-ctx.Done():
			return domain.AgentReadinessSnapshot{}, ctx.Err()
		}
	}
	if presenceOnly {
		if call := c.calls[readinessCallKey{id: id}]; call != nil {
			snapshot := c.snapshotLocked(entry, purpose)
			// A prior installed state is not proof while an execution-enabled
			// refresh is still replacing it. Presence callers must not inherit
			// that in-flight call or report the stale state as installed.
			snapshot.Installation.State = domain.AgentInstallationUnknown
			snapshot.EffectiveReadiness = domain.EffectiveAgentReadiness(snapshot.Installation.State, snapshot.Authentication.State)
			c.mu.Unlock()
			c.logDecision(id, purpose, "presence_in_flight_execution", 0, snapshot, "", time.Time{})
			return snapshot, nil
		}
	} else if call := c.calls[readinessCallKey{id: id, presenceOnly: true}]; call != nil {
		// Presence and identity checks have different guarantees. Let a normal
		// caller wait for the process-free pass to finish, then retry so an
		// identity-pending result cannot satisfy the normal check from cache.
		c.mu.Unlock()
		select {
		case <-call.done:
			return c.ensureOne(ctx, id, purpose, requested, false)
		case <-ctx.Done():
			return domain.AgentReadinessSnapshot{}, ctx.Err()
		}
	}
	call := &readinessCall{
		key:            key,
		done:           make(chan struct{}),
		checks:         needed,
		installVersion: entry.installVersion,
		authVersion:    entry.authVersion,
	}
	c.calls[key] = call
	entry.checking = needed
	started := c.now()
	c.mu.Unlock()
	go c.runCheck(id, purpose, needed, presenceOnly, requested&readinessInvalidateAuthentication != 0, call, started)

	select {
	case <-call.done:
		c.mu.Lock()
		snapshot := c.snapshotLocked(c.entries[id], purpose)
		c.mu.Unlock()
		return snapshot, nil
	case <-ctx.Done():
		return domain.AgentReadinessSnapshot{}, ctx.Err()
	}
}

func (c *readinessCoordinator) Invalidate(agentID string, invalidation readinessInvalidation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry := c.entries[agentID]
	if entry == nil {
		return
	}
	entry.invalidated |= invalidation
	if invalidation&readinessInvalidateInstallation != 0 {
		entry.installVersion++
		entry.snapshot.Installation.Freshness = domain.AgentReadinessStale
	}
	if invalidation&readinessInvalidateAuthentication != 0 {
		entry.authVersion++
		entry.snapshot.Authentication.Freshness = domain.AgentReadinessStale
	}
	entry.nextRetryAt = time.Time{}
}

func (c *readinessCoordinator) Warm() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Warm installation through the process-free capability first. Goose's
		// normal resolver validates identity by running --help, so startup must
		// not accidentally turn this background cache fill into an execution
		// probe. Adapters without a presence capability retain their existing
		// installation check behavior.
		_, _ = c.ensureMode(c.ctx, nil, domain.AgentReadinessPurposeDisplay, readinessInvalidateInstallation, true)
		// Preserve the warm authentication pass without asking adapters to
		// resolve installation again. An identity-pending installation yields an
		// inconclusive auth observation until an explicit normal refresh.
		_, _ = c.ensureMode(c.ctx, nil, domain.AgentReadinessPurposeDisplay, readinessInvalidateAuthentication, false)
	}()
	return done
}

func (c *readinessCoordinator) normalizeIDs(ids []string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(ids) == 0 {
		return c.sortedIDsLocked(), nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := c.entries[id]; !ok {
			return nil, unsupportedAgentError{id: id}
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (c *readinessCoordinator) sortedIDsLocked() []string {
	ids := make([]string, 0, len(c.entries))
	for id := range c.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (c *readinessCoordinator) neededChecksLocked(entry *readinessEntry, purpose domain.AgentReadinessPurpose) readinessInvalidation {
	ttl := c.displayTTL
	if purpose == domain.AgentReadinessPurposeLaunch {
		ttl = c.launchTTL
	}
	now := c.now()
	needed := entry.invalidated
	if entry.snapshot.Installation.CheckedAt == nil || now.Sub(*entry.snapshot.Installation.CheckedAt) >= ttl {
		needed |= readinessInvalidateInstallation
	}
	if entry.snapshot.Authentication.CheckedAt == nil || now.Sub(*entry.snapshot.Authentication.CheckedAt) >= ttl {
		needed |= readinessInvalidateAuthentication
	}
	return needed
}

func (c *readinessCoordinator) snapshotLocked(entry *readinessEntry, purpose domain.AgentReadinessPurpose) domain.AgentReadinessSnapshot {
	snapshot := entry.snapshot
	needed := c.neededChecksLocked(entry, purpose)
	if needed&readinessInvalidateInstallation != 0 {
		snapshot.Installation.Freshness = domain.AgentReadinessStale
	}
	if needed&readinessInvalidateAuthentication != 0 {
		snapshot.Authentication.Freshness = domain.AgentReadinessStale
	}
	if entry.checking&readinessInvalidateInstallation != 0 {
		snapshot.Installation.Freshness = domain.AgentReadinessChecking
		snapshot.Installation.ReasonCode = domain.AgentReadinessReasonChecking
		snapshot.Installation.Reason = "Installation is being checked."
	}
	if entry.checking&readinessInvalidateAuthentication != 0 {
		snapshot.Authentication.Freshness = domain.AgentReadinessChecking
		snapshot.Authentication.ReasonCode = domain.AgentReadinessReasonChecking
		snapshot.Authentication.Reason = "Authentication is being checked."
	}
	snapshot.EffectiveReadiness = domain.EffectiveAgentReadiness(snapshot.Installation.State, snapshot.Authentication.State)
	return snapshot
}

func (c *readinessCoordinator) freshAgent(id string) (ports.HarnessAgent, bool) {
	for _, item := range c.factory() {
		if string(item.Harness) == id {
			return item, true
		}
	}
	return ports.HarnessAgent{}, false
}

func (c *readinessCoordinator) scheduleRetry(id string, generation uint64, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-c.ctx.Done():
		return
	}
	c.mu.Lock()
	entry := c.entries[id]
	valid := entry != nil && entry.retryGeneration == generation && !entry.nextRetryAt.IsZero()
	if valid {
		entry.nextRetryAt = time.Time{}
	}
	c.mu.Unlock()
	if valid {
		_, _ = c.Ensure(c.ctx, []string{id}, domain.AgentReadinessPurposeDisplay)
	}
}

func (c *readinessCoordinator) logDecision(id string, purpose domain.AgentReadinessPurpose, decision string, duration time.Duration, snapshot domain.AgentReadinessSnapshot, failureCategory string, nextRetry time.Time) {
	args := []any{
		"harness", id, "purpose", purpose, "decision", decision,
		"duration_ms", duration.Milliseconds(), "outcome", snapshot.EffectiveReadiness,
	}
	if failureCategory != "" {
		args = append(args, "failure_category", failureCategory)
	}
	if !nextRetry.IsZero() {
		args = append(args, "next_retry", nextRetry)
	}
	c.logger.Debug("agent readiness check", args...)
}
