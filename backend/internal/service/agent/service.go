package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/agentaccounts/codex"
)

var (
	modelCatalogLoadTimeout = 30 * time.Second
	// Retained for compatibility with focused tests that construct an obviously
	// old timestamp; production freshness is calendar-day based below.
	modelCatalogTrustWindow = 6 * time.Hour
	// How long a cached catalog is trusted before Tallpine asks a cache-first client to
	// revalidate in the background. Long, because rediscovery runs an agent CLI:
	// this covers drift a fingerprint cannot see, not routine correctness.
	modelCatalogMonitorInterval = time.Minute
	modelCatalogWakeThreshold   = 3 * time.Minute
	modelCatalogMaxRetries      = 3
	modelCatalogRetryDelays     = [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
)

// catalogNeedsRevalidation uses the machine's calendar date, not an elapsed
// duration. A catalog remains fresh through the user's local day and becomes
// due after midnight, including after timezone changes or sleep/wake.
func catalogNeedsRevalidation(lastSuccess, now time.Time) bool {
	if lastSuccess.IsZero() {
		return true
	}
	y, m, d := now.Date()
	ly, lm, ld := lastSuccess.In(now.Location()).Date()
	return y != ly || m != lm || d != ld
}

func catalogClockDiscontinuity(previous, now time.Time, previousZone string, previousOffset int) bool {
	zone, offset := now.Zone()
	return catalogNeedsRevalidation(previous, now) || now.Sub(previous) > modelCatalogWakeThreshold || zone != previousZone || offset != previousOffset || now.Before(previous)
}

type modelLoadMode uint8

const (
	modelLoadCached modelLoadMode = iota
	modelLoadRevalidate
	modelLoadRefresh
)

type modelCatalogCall struct {
	done       chan struct{}
	refresh    chan struct{} // closed under modelCallMu when an explicit refresh joins
	catalog    ports.AgentModelCatalog
	err        error
	generation int64
}

// Service owns normalized harness readiness and the unchanged model catalog.
// Consumers share coordinator checks instead of probing adapters directly.
type Service struct {
	agents            []ports.HarnessAgent
	readiness         *readinessCoordinator
	codexAccounts     *codex.Service
	cache             ports.AgentModelCatalogCache
	discoverer        ports.AgentModelDiscoverer
	modelDiscoveryDir string
	modelDiscoveryEnv func(context.Context, string) (map[string]string, error)
	fingerprintEnv    func(context.Context, string) (map[string]string, error)
	sessions          SessionUsageLookup
	resolverMu        map[string]*sync.Mutex
	modelCallMu       sync.Mutex
	modelCalls        map[string]*modelCatalogCall
	modelGeneration   map[string]int64
	discoverySlots    chan struct{}
	// inputChecks tracks background credential-input checks for cached
	// catalogs, so a cache read never waits on them.
	inputCheckMu sync.Mutex
	inputChecks  map[string]catalogInputCheck
	ctx          context.Context
	now          func() time.Time
	logger       *slog.Logger
	// readinessWarm closes once the startup readiness pass finishes. Catalog
	// updates wait for it; reads of the saved catalog never do.
	readinessWarm     chan struct{}
	readinessWarmOnce sync.Once
	// modelGate adjusts every catalog read; see ModelGate.
	modelGate ModelGate
}

// New returns an agent service with no adapters (tests); the daemon passes Deps.Registry.
func New() *Service {
	return NewWithDeps(Deps{})
}

// NewWithDeps returns the production service with in-memory readiness and a
// durable model-catalog cache.
func NewWithDeps(deps Deps) *Service {
	registry := deps.Registry
	if registry == nil {
		registry = func() []ports.HarnessAgent { return nil }
	}
	agents := registry()
	svc := newService(agents, deps.Cache, deps.Discoverer)
	svc.modelDiscoveryDir = deps.ModelDiscoveryDir
	svc.modelDiscoveryEnv = deps.ModelDiscoveryEnv
	svc.fingerprintEnv = deps.ModelDiscoveryFingerprintEnv
	if svc.fingerprintEnv == nil {
		svc.fingerprintEnv = deps.ModelDiscoveryEnv
	}
	svc.readinessWarm = make(chan struct{})
	if deps.Logger != nil {
		svc.logger = deps.Logger
	}
	svc.modelGate = deps.ModelGate
	svc.codexAccounts = codex.NewService(codex.Deps{
		Context: deps.Context, Logger: deps.Logger, Clock: deps.Clock,
		AccountRoot: deps.CodexAccountRoot, PendingRoot: deps.CodexPendingRoot,
		SwitchStagingRoot: deps.CodexSwitchStagingRoot, GlobalHome: deps.CodexGlobalHome,
		Factory: deps.CodexAccounts, OperationGate: deps.CodexOperationGate, Switches: deps.CodexAccountSwitches, AccountStore: deps.CodexAccountStore,
		Installation: func(ctx context.Context) (domain.AgentInstallationObservation, error) {
			snapshots, err := svc.readiness.EnsureInstallation(ctx, []string{string(domain.HarnessCodex)}, domain.AgentReadinessPurposeDisplay)
			if err != nil {
				return domain.AgentInstallationObservation{}, err
			}
			return snapshots[0].Installation, nil
		},
		InvalidateAuthentication: func() {
			svc.readiness.Invalidate(string(domain.HarnessCodex), readinessInvalidateAuthentication)
		},
	})
	svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{
		Agents: agents, Factory: registry, Context: deps.Context, Logger: deps.Logger,
		AuthenticationCheck: svc.codexAccounts.AuthenticationWithAccount,
		// A catalog built while the agent was signed out carries that failure
		// (a rejected-credential warning or bare fallback aliases). A detected
		// login must rediscover it without anyone pressing refresh.
		OnAuthenticationRecovered: svc.InvalidateModelCatalogs,
	})
	svc.sessions = deps.Sessions
	if deps.Context != nil {
		svc.ctx = deps.Context
	}
	if deps.Clock != nil {
		svc.now = deps.Clock
	}
	return svc
}

// NewWithAgents returns an agent service over a caller-provided adapter slice.
// It is used by focused tests.
func NewWithAgents(agents []ports.HarnessAgent) *Service {
	svc := newService(agents, nil, nil)
	svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{Agents: agents})
	return svc
}

func newService(agents []ports.HarnessAgent, cache ports.AgentModelCatalogCache, discoverer ports.AgentModelDiscoverer) *Service {
	resolverMu := make(map[string]*sync.Mutex, len(agents))
	for _, item := range agents {
		resolverMu[string(item.Harness)] = &sync.Mutex{}
	}
	return &Service{codexAccounts: codex.NewService(codex.Deps{}), agents: agents, readiness: newReadinessCoordinator(readinessCoordinatorConfig{Agents: agents}), cache: cache, discoverer: discoverer, resolverMu: resolverMu, modelCalls: map[string]*modelCatalogCall{}, modelGeneration: map[string]int64{}, discoverySlots: make(chan struct{}, 2), ctx: context.Background(), now: time.Now, logger: slog.Default()}
}

// Codex returns the Codex account service: login, logout, switch and capacity
// of Codex accounts. It is never nil.
func (s *Service) Codex() *codex.Service { return s.codexAccounts }

// WarmModelCatalogs starts the bounded cache scheduler. Readiness is never held
// up by model discovery; at most two adapter discoveries run at once.
func (s *Service) WarmModelCatalogs(ctx context.Context) {
	if s.cache == nil || s.discoverer == nil {
		return
	}
	go func() {
		s.prefetchModelCatalogs(ctx, false)
		s.monitorModelCatalogFreshness(ctx)
	}()
}

func (s *Service) prefetchModelCatalogs(ctx context.Context, force bool) {
	scopeCache, ok := s.cache.(ports.AgentModelCatalogScopeCache)
	var records []ports.CachedAgentModelCatalog
	var err error
	if ok {
		records, err = scopeCache.ListAgentModelCatalogs(ctx)
	} else {
		for _, item := range s.agents {
			rows, listErr := s.cache.ListAgentModelCatalogsByAgent(ctx, string(item.Harness))
			if listErr != nil {
				err = listErr
				break
			}
			records = append(records, rows...)
		}
	}
	if err != nil || ctx.Err() != nil {
		return
	}
	readiness, err := s.readiness.EnsureInstallation(ctx, nil, domain.AgentReadinessPurposeDisplay)
	if err != nil {
		return
	}
	installed := make(map[string]struct{}, len(readiness))
	for _, item := range readiness {
		if item.Installation.State == domain.AgentInstallationInstalled {
			installed[item.ID] = struct{}{}
		}
	}
	// Retain the newest row for every device and credential scope and seed one
	// device job for installed adapters that have never been discovered.
	latest := make(map[string]ports.CachedAgentModelCatalog, len(records)+len(installed))
	for _, record := range records {
		// Older builds persisted per-project and Settings catalogs. They are no
		// longer read.
		if !activeModelCatalogScope(record.ProjectID) {
			continue
		}
		key := record.AgentID + "\x00" + record.ProjectID
		current, exists := latest[key]
		if !exists || record.FetchedAt.After(current.FetchedAt) {
			latest[key] = record
		}
	}
	records = records[:0]
	for agentID := range installed {
		key := agentID + "\x00"
		if _, exists := latest[key]; !exists {
			latest[key] = ports.CachedAgentModelCatalog{AgentID: agentID}
		}
	}
	for _, record := range latest {
		records = append(records, record)
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].ProjectID == records[j].ProjectID {
			return records[i].AgentID < records[j].AgentID
		}
		return records[i].ProjectID < records[j].ProjectID
	})
	jobs := make(chan ports.CachedAgentModelCatalog, len(records))
	for _, record := range records {
		if _, eligible := installed[record.AgentID]; !eligible {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if force || catalogNeedsRevalidation(record.LastSuccessAt, s.now()) || record.RefreshState != "idle" {
			jobs <- record
		}
	}
	close(jobs)
	var workers sync.WaitGroup
	for range cap(s.discoverySlots) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for record := range jobs {
				if ctx.Err() != nil {
					return
				}
				_, _ = s.revalidateModels(ctx, record.AgentID, record.ProjectID)
			}
		}()
	}
	workers.Wait()
}

func (s *Service) monitorModelCatalogFreshness(ctx context.Context) {
	ticker := time.NewTicker(modelCatalogMonitorInterval)
	defer ticker.Stop()
	last := s.now()
	lastZone, lastOffset := last.Zone()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := s.now()
			zone, offset := now.Zone()
			if catalogClockDiscontinuity(last, now, lastZone, lastOffset) {
				s.prefetchModelCatalogs(ctx, true)
			}
			last, lastZone, lastOffset = now, zone, offset
		}
	}
}

// Models returns one normalized model catalog, stamped with when this user last
// ran each model. Catalogs are device-wide: a project ID is accepted for API
// compatibility and ignored, so every project shares one catalog per agent and
// a picker opened in a new project never waits on discovery. Only a cloud
// credential scope (credentialScopePrefix) caches separately. Cached values
// survive daemon restarts; refresh forces a new documented CLI discovery
// attempt. Discovery failures degrade to the last cached catalog or a custom
// model input.
func (s *Service) Models(ctx context.Context, agentID, scope string, refresh bool) (ports.AgentModelCatalog, error) {
	catalog, err := s.modelCatalog(ctx, agentID, scope, refresh)
	if err != nil {
		return catalog, err
	}
	return s.gateModels(ctx, agentID, s.withModelUsage(ctx, agentID, catalog)), nil
}

// ModelCatalog returns the persisted catalog without session-history enrichment.
// Settings reads the same harness-reported catalog as task pickers; recency
// belongs to task pickers. Cached reads never wait for CLI discovery or input
// checks, which run in the background. A scope with no persisted catalog still
// needs its first discovery.
func (s *Service) ModelCatalog(ctx context.Context, agentID, projectID string, refresh bool) (ports.AgentModelCatalog, error) {
	catalog, err := s.modelCatalog(ctx, agentID, projectID, refresh)
	if err != nil {
		return catalog, err
	}
	return s.gateModels(ctx, agentID, catalog), nil
}

func (s *Service) modelCatalog(ctx context.Context, agentID, scope string, refresh bool) (ports.AgentModelCatalog, error) {
	if s.discoverer == nil {
		return ports.AgentModelCatalog{}, apierr.Internal("MODEL_DISCOVERY_UNAVAILABLE", "Model discovery is unavailable")
	}
	projectID := modelCatalogScope(scope)
	if !refresh {
		if _, ok := s.agent(agentID); !ok {
			return ports.AgentModelCatalog{}, apierr.NotFound("AGENT_NOT_FOUND", "Unknown agent adapter")
		}
		cached, ok, err := s.cachedCatalog(ctx, agentID, projectID)
		if err != nil {
			return ports.AgentModelCatalog{}, err
		}
		if ok {
			// Claude provider model IDs are credential-scoped. The cache is served
			// at once and its local discovery inputs are checked in the background;
			// while that runs the catalog reports itself refreshing, so open views
			// re-read it and pick up the rediscovered catalog when inputs changed.
			inputsChecking := agentID == "claude-code" && s.checkCatalogInputsInBackground(agentID, projectID, cached.BinaryVersion)
			cached.Catalog = applyCustomModelEntryPolicy(cached.Catalog, s.discoverer.Manual(agentID))
			due := catalogNeedsRevalidation(catalogLastSuccess(cached.Catalog), s.now())
			needsRecovery := cached.RefreshState == "refreshing"
			retriesExhausted := modelCatalogRetriesExhausted(cached)
			cached.Catalog.RefreshRecommended = !retriesExhausted && (due || needsRecovery || cached.RefreshState == "error" || cached.RefreshState == "queued")
			if !retriesExhausted && (due || needsRecovery) && (cached.RetryAt.IsZero() || !s.now().Before(cached.RetryAt)) {
				go func() { _, _ = s.revalidateModels(s.ctx, agentID, projectID) }()
			} else if retriesExhausted {
				go s.revalidateChangedInputs(agentID, projectID, cached.BinaryVersion)
			} else if !due && agentID != "claude-code" {
				time.AfterFunc(10*time.Millisecond, func() {
					s.revalidateChangedInputs(agentID, projectID, cached.BinaryVersion)
				})
			}
			if inputsChecking && cached.Catalog.RefreshState == "" {
				cached.Catalog.RefreshState = "refreshing"
			}
			return cached.Catalog, nil
		}
		return s.firstModelCatalog(ctx, agentID, projectID)
	}
	return s.coalesceModelLoad(ctx, agentID, projectID, modelLoadRefresh)
}

// modelUpdateReadinessWait bounds how long a background catalog update waits for the
// startup readiness pass, so a daemon that never warms readiness still updates.
var modelUpdateReadinessWait = 30 * time.Second

// waitForStartupReadiness holds a background load until the startup readiness pass
// finishes, at most modelUpdateReadinessWait. Reads of the saved catalog never
// come here, so the UI lists models without waiting on readiness. It runs
// before the discovery slot and timeout, which keep their full budget. An
// explicit refresh bypasses the wait, including when it joins a background load.
func (s *Service) waitForStartupReadiness(ctx context.Context, refresh <-chan struct{}) {
	if s.readinessWarm == nil {
		return
	}
	timer := time.NewTimer(modelUpdateReadinessWait)
	defer timer.Stop()
	select {
	case <-s.readinessWarm:
	case <-refresh:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// readyForModelUpdate reports whether readiness lets a catalog update discover
// the agent. Only a fresh not-installed observation skips the update; an
// unknown installation still discovers, and sign-in is left to discovery, which
// keeps the saved catalog while an agent is signed out.
func (s *Service) readyForModelUpdate(agentID string) bool {
	if s.readiness == nil {
		return true
	}
	for _, snapshot := range s.readiness.Snapshot() {
		if snapshot.ID == agentID {
			return snapshot.Installation.State != domain.AgentInstallationNotInstalled || snapshot.Installation.Freshness != domain.AgentReadinessFresh
		}
	}
	return true
}

// modelCatalogFirstLoadWait bounds how long a read with no persisted catalog
// waits for its first discovery. Fast adapters answer within it; a slower one
// returns its capability fallback at once while discovery finishes.
var modelCatalogFirstLoadWait = 500 * time.Millisecond

// firstModelCatalog serves a scope that has never been discovered without
// blocking the picker. Discovery runs on the service context, so it completes
// and persists even after the request returns; the placeholder reports itself
// refreshing, so open views re-read and pick up the discovered catalog.
func (s *Service) firstModelCatalog(ctx context.Context, agentID, scope string) (ports.AgentModelCatalog, error) {
	type result struct {
		catalog ports.AgentModelCatalog
		err     error
	}
	done := make(chan result, 1)
	go func() {
		catalog, err := s.coalesceModelLoad(s.ctx, agentID, scope, modelLoadCached)
		done <- result{catalog: catalog, err: err}
	}()
	timer := time.NewTimer(modelCatalogFirstLoadWait)
	defer timer.Stop()
	select {
	case first := <-done:
		return first.catalog, first.err
	case <-ctx.Done():
		return ports.AgentModelCatalog{}, ctx.Err()
	case <-timer.C:
	}
	placeholder := s.discoverer.Manual(agentID)
	placeholder.RefreshState = "refreshing"
	return placeholder, nil
}

// catalogInputCheckTTL is how long a matching input check stands for a cached
// catalog. Logins and logouts invalidate catalogs directly, so this only bounds
// how long an out-of-band credential change goes unnoticed.
const catalogInputCheckTTL = 2 * time.Minute

type catalogInputCheck struct {
	running     bool
	checkedAt   time.Time
	fingerprint string
}

// checkCatalogInputsInBackground starts, at most once at a time and once per
// catalogInputCheckTTL, a check of whether a cached catalog's discovery inputs
// changed, rediscovering the catalog when they did. It reports whether a check
// is in flight.
func (s *Service) checkCatalogInputsInBackground(agentID, projectID, fingerprint string) bool {
	key := agentID + "\x00" + projectID
	s.inputCheckMu.Lock()
	if s.inputChecks == nil {
		s.inputChecks = make(map[string]catalogInputCheck)
	}
	check := s.inputChecks[key]
	if check.running {
		s.inputCheckMu.Unlock()
		return true
	}
	if check.fingerprint == fingerprint && !check.checkedAt.IsZero() && s.now().Sub(check.checkedAt) < catalogInputCheckTTL {
		s.inputCheckMu.Unlock()
		return false
	}
	s.inputChecks[key] = catalogInputCheck{running: true}
	s.inputCheckMu.Unlock()
	go func() {
		current := fingerprint
		if s.ctx.Err() == nil && s.modelCatalogInputsChanged(s.ctx, agentID, projectID, fingerprint) {
			_, _ = s.revalidateModels(s.ctx, agentID, projectID)
			if cached, ok, err := s.cachedCatalog(s.ctx, agentID, projectID); err == nil && ok {
				current = cached.BinaryVersion
			}
		}
		s.inputCheckMu.Lock()
		s.inputChecks[key] = catalogInputCheck{checkedAt: s.now(), fingerprint: current}
		s.inputCheckMu.Unlock()
	}()
	return true
}

func (s *Service) revalidateChangedInputs(agentID, projectID, cachedFingerprint string) {
	if s.ctx.Err() != nil {
		return
	}
	if s.modelCatalogInputsChanged(s.ctx, agentID, projectID, cachedFingerprint) {
		_, _ = s.revalidateModels(s.ctx, agentID, projectID)
	}
}

func (s *Service) modelCatalogInputsChanged(ctx context.Context, agentID, projectID, cachedFingerprint string) bool {
	item, ok := s.agent(agentID)
	if !ok {
		return false
	}
	var binary string
	if resolver, ok := item.Agent.(ports.AgentBinaryResolver); ok {
		lock := s.resolverMu[agentID]
		lock.Lock()
		resolved, err := resolver.ResolveBinary(ctx)
		lock.Unlock()
		if err != nil {
			return false
		}
		binary = resolved
	}
	request, err := s.modelDiscoveryRequestEnv(ctx, agentID, projectID, binary, s.fingerprintEnv)
	if err != nil {
		return false
	}
	return s.discoverer.CatalogFingerprint(ctx, request) != cachedFingerprint
}

// credentialScopePrefix marks a model-catalog scope that is not a project but a
// cloud credential kind. A cloud session has no local project, yet its picker
// must show the models the pushed credential can run; encoding the credential
// type into the scope caches each provider's catalog separately with no schema
// change. '@' and ':' cannot appear in a real project ID (projectIDPattern), so
// the project and credential namespaces never collide.
const credentialScopePrefix = "@cred:"

// credentialTypeFromScope returns the credential type a scope carries, if any.
func credentialTypeFromScope(scope string) (string, bool) {
	rest, ok := strings.CutPrefix(scope, credentialScopePrefix)
	if !ok || strings.TrimSpace(rest) == "" {
		return "", false
	}
	return rest, true
}

// modelCatalogScope maps a caller's scope to the cache scope. A credential scope
// has no backing project and caches under its own key; anything else, including
// a project ID, is the single device scope.
func modelCatalogScope(scope string) string {
	if _, ok := credentialTypeFromScope(scope); ok {
		return scope
	}
	return ""
}

// activeModelCatalogScope reports whether a persisted scope is still read: the
// device scope or a credential scope. Older builds also wrote per-project rows.
func activeModelCatalogScope(scope string) bool {
	return modelCatalogScope(scope) == scope
}

func (s *Service) modelDiscoveryRequest(ctx context.Context, agentID, projectID, binary string) (ports.AgentModelDiscoveryRequest, error) {
	return s.modelDiscoveryRequestEnv(ctx, agentID, projectID, binary, s.modelDiscoveryEnv)
}

func (s *Service) modelDiscoveryRequestEnv(ctx context.Context, agentID, projectID, binary string, envFn func(context.Context, string) (map[string]string, error)) (ports.AgentModelDiscoveryRequest, error) {
	request := ports.AgentModelDiscoveryRequest{AgentID: agentID, Binary: binary}
	// Credential-scoped discovery reflects a cloud session's pushed credential,
	// not a local project: leave WorkingDir/Env empty and let the adapter unlock
	// that provider's models from the credential type alone.
	if credentialType, ok := credentialTypeFromScope(projectID); ok {
		request.CredentialType = credentialType
		return request, nil
	}
	if envFn != nil {
		env, err := envFn(ctx, agentID)
		if err != nil {
			return ports.AgentModelDiscoveryRequest{}, fmt.Errorf("resolve model discovery account: %w", err)
		}
		request.Env = env
	}
	return s.globalModelDiscoveryRequest(request)
}

func (s *Service) globalModelDiscoveryRequest(request ports.AgentModelDiscoveryRequest) (ports.AgentModelDiscoveryRequest, error) {
	if s.modelDiscoveryDir == "" {
		return request, nil
	}
	if !filepath.IsAbs(s.modelDiscoveryDir) {
		return ports.AgentModelDiscoveryRequest{}, fmt.Errorf("model discovery directory must be absolute: %s", s.modelDiscoveryDir)
	}
	if err := os.MkdirAll(s.modelDiscoveryDir, 0o700); err != nil {
		return ports.AgentModelDiscoveryRequest{}, fmt.Errorf("create model discovery directory: %w", err)
	}
	request.WorkingDir = s.modelDiscoveryDir
	return request, nil
}

// RevalidateModels rediscovers a cache-first catalog after the normal read path
// marks it old enough to refresh in the background.
func (s *Service) RevalidateModels(ctx context.Context, agentID, scope string) (ports.AgentModelCatalog, error) {
	catalog, err := s.revalidateModels(ctx, agentID, scope)
	if err != nil {
		return catalog, err
	}
	return s.gateModels(ctx, agentID, s.withModelUsage(ctx, agentID, catalog)), nil
}

// revalidateModels is the unstamped variant for background callers that discard
// the catalog and should not pay for a session-history scan.
func (s *Service) revalidateModels(ctx context.Context, agentID, scope string) (ports.AgentModelCatalog, error) {
	return s.coalesceModelLoad(ctx, agentID, modelCatalogScope(scope), modelLoadRevalidate)
}

// InvalidateModelCatalogs marks existing scopes due and schedules cache-first
// revalidation. The last successful choices remain visible throughout.
func (s *Service) InvalidateModelCatalogs(agentID string) {
	if s.cache == nil {
		return
	}
	go func() {
		records, err := s.cache.ListAgentModelCatalogsByAgent(s.ctx, agentID)
		if err != nil {
			return
		}
		for _, record := range records {
			if !activeModelCatalogScope(record.ProjectID) {
				continue
			}
			if s.ctx.Err() != nil {
				return
			}
			var catalog ports.AgentModelCatalog
			if json.Unmarshal([]byte(record.CatalogJSON), &catalog) == nil {
				s.requeueCatalog(record.ProjectID, catalog)
			}
			_, _ = s.revalidateModels(s.ctx, agentID, record.ProjectID)
		}
	}()
}

// requeueCatalog marks a cached scope due and drops its retry backoff, so the
// next revalidation runs discovery. The last successful choices remain visible.
func (s *Service) requeueCatalog(projectID string, catalog ports.AgentModelCatalog) {
	catalog.RefreshRecommended = true
	catalog.RefreshState = "queued"
	catalog.RefreshError = ""
	// The warning described the inputs being invalidated (for example a
	// rejected login). Clear it so clients stop showing it while the new
	// discovery runs; a repeat failure restores it.
	catalog.Warning = ""
	catalog.WarningCode = ""
	catalog.LastSuccessAt = nil
	catalog.RetryAt = nil
	_ = s.saveCatalog(s.ctx, projectID, catalog, time.Now().UTC().UnixNano(), 0)
}

func (s *Service) agent(agentID string) (ports.HarnessAgent, bool) {
	for _, item := range s.agents {
		if string(item.Harness) == agentID {
			return item, true
		}
	}
	return ports.HarnessAgent{}, false
}

// ResolveAgentBinary resolves one harness through its shipped adapter. This is
// the shared boundary for features that must launch the same executable normal
// session startup recognizes, including managed locations outside PATH.
func (s *Service) ResolveAgentBinary(ctx context.Context, agentID string) (string, error) {
	item, ok := s.agent(agentID)
	if !ok {
		return "", apierr.Invalid("AGENT_UNKNOWN", fmt.Sprintf("unknown agent %q", agentID), nil)
	}
	resolver, ok := item.Agent.(ports.AgentBinaryResolver)
	if !ok {
		return "", fmt.Errorf("agent %s: %w", agentID, ports.ErrAgentBinaryNotFound)
	}
	lock := s.resolverMu[agentID]
	lock.Lock()
	defer lock.Unlock()
	return resolver.ResolveBinary(ctx)
}
