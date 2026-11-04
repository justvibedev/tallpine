package agent

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (s *Service) coalesceModelLoad(
	ctx context.Context,
	agentID, projectID string,
	mode modelLoadMode,
) (ports.AgentModelCatalog, error) {
	key := agentID + "\x00" + projectID
	s.modelCallMu.Lock()
	if active := s.modelCalls[key]; active != nil {
		if mode == modelLoadRefresh {
			select {
			case <-active.refresh:
			default:
				close(active.refresh)
			}
		}
		s.modelCallMu.Unlock()
		select {
		case <-active.done:
			return active.catalog, active.err
		case <-ctx.Done():
			return ports.AgentModelCatalog{}, ctx.Err()
		}
	}
	wallGeneration := time.Now().UTC().UnixNano()
	if s.modelGeneration[key] < wallGeneration {
		s.modelGeneration[key] = wallGeneration
	} else {
		s.modelGeneration[key]++
	}
	call := &modelCatalogCall{done: make(chan struct{}), refresh: make(chan struct{}), generation: s.modelGeneration[key]}
	if mode == modelLoadRefresh {
		close(call.refresh)
	}
	s.modelCalls[key] = call
	s.modelCallMu.Unlock()

	baseCtx := s.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	go func() {
		// Wait for readiness before taking a discovery slot or starting the
		// discovery timeout, so a slow startup neither holds slots nor eats into
		// the discovery's own budget. An explicit refresh also wakes an existing
		// background load so spawn validation does not wait on startup readiness.
		s.waitForStartupReadiness(baseCtx, call.refresh)
		select {
		case s.discoverySlots <- struct{}{}:
			defer func() { <-s.discoverySlots }()
		case <-baseCtx.Done():
			call.err = baseCtx.Err()
			s.modelCallMu.Lock()
			delete(s.modelCalls, key)
			close(call.done)
			s.modelCallMu.Unlock()
			return
		}
		loadCtx, cancel := context.WithTimeout(baseCtx, modelCatalogLoadTimeout)
		defer cancel()
		select {
		case <-call.refresh:
			mode = modelLoadRefresh
		default:
		}
		call.catalog, call.err = s.loadModels(loadCtx, agentID, projectID, mode, call.generation)
		s.modelCallMu.Lock()
		delete(s.modelCalls, key)
		close(call.done)
		s.modelCallMu.Unlock()
	}()

	select {
	case <-call.done:
		return call.catalog, call.err
	case <-ctx.Done():
		return ports.AgentModelCatalog{}, ctx.Err()
	}
}

func (s *Service) loadModels(ctx context.Context, agentID, projectID string, mode modelLoadMode, generation int64) (ports.AgentModelCatalog, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentModelCatalog{}, err
	}
	item, ok := s.agent(agentID)
	if !ok {
		return ports.AgentModelCatalog{}, apierr.NotFound("AGENT_NOT_FOUND", "Unknown agent adapter")
	}
	if s.discoverer == nil {
		return ports.AgentModelCatalog{}, apierr.Internal("MODEL_DISCOVERY_UNAVAILABLE", "Model discovery is unavailable")
	}
	cached, hasCached, err := s.cachedCatalog(ctx, agentID, projectID)
	if err != nil {
		return ports.AgentModelCatalog{}, err
	}
	cached.ProjectID = projectID
	policy := s.discoverer.Manual(agentID)
	if hasCached {
		cached.Catalog = applyCustomModelEntryPolicy(cached.Catalog, policy)
	}
	var binary string
	if resolver, ok := item.Agent.(ports.AgentBinaryResolver); ok {
		lock := s.resolverMu[agentID]
		lock.Lock()
		resolved, err := resolver.ResolveBinary(ctx)
		lock.Unlock()
		if err == nil {
			binary = resolved
		}
	}
	request, err := s.modelDiscoveryRequest(ctx, agentID, projectID, binary)
	if err != nil {
		return ports.AgentModelCatalog{}, err
	}
	// Fingerprints the same inputs the discovery run would read, so a change to
	// either the executable or the configuration behind it invalidates the cache.
	version := s.discoverer.CatalogFingerprint(ctx, request)
	inputsChanged := hasCached && cached.BinaryVersion != version
	explicitlyInvalidated := hasCached && (cached.RefreshState == "queued" || cached.RefreshState == "refreshing")
	if hasCached && mode == modelLoadCached && cached.BinaryVersion == version {
		// A command-backed catalog can drift without the binary or its config
		// changing (a provider adds a model), which no fingerprint can see. Ask
		// cache-first clients to revalidate in the background once the catalog is
		// old enough, so staleness resolves itself instead of waiting for someone
		// to press a refresh button.
		cached.Catalog.RefreshRecommended = !modelCatalogRetriesExhausted(cached) && catalogNeedsRevalidation(catalogLastSuccess(cached.Catalog), s.now())
		return cached.Catalog, nil
	}

	if mode != modelLoadRefresh && hasCached && !inputsChanged && !explicitlyInvalidated && modelCatalogRetriesExhausted(cached) {
		cached.Catalog.RefreshRecommended = false
		return cached.Catalog, nil
	}
	if mode != modelLoadRefresh && hasCached && !inputsChanged && !explicitlyInvalidated && !cached.RetryAt.IsZero() && s.now().Before(cached.RetryAt) {
		cached.Catalog.RefreshState = "error"
		cached.Catalog.RefreshError = cached.RefreshError
		cached.Catalog.RetryAt = modelCatalogRetryAt(cached.RetryAt)
		cached.Catalog.RefreshRecommended = true
		return cached.Catalog, nil
	}
	if !s.readyForModelUpdate(agentID) {
		// Readiness found nothing to discover with: keep serving the saved catalog
		// rather than recording a failure over it.
		if hasCached {
			return cached.Catalog, nil
		}
		fallback := policy
		fallback.BinaryVersion = version
		return fallback, nil
	}
	if mode == modelLoadRefresh || inputsChanged || explicitlyInvalidated {
		cached.RetryCount = 0
		cached.RetryAt = time.Time{}
		cached.RefreshError = ""
		cached.Catalog.RetryAt = nil
	}
	// Only an explicit user refresh surfaces a loading state. Automatic daily,
	// wake, and input-change revalidation keeps the last-known-good catalog
	// visible without causing cache subscribers to show a loader.
	if mode == modelLoadRefresh {
		_ = s.persistCatalogState(ctx, cached, hasCached, "refreshing", "", time.Time{}, generation)
	}
	discovered, discoverErr := s.discoverer.Discover(ctx, request)
	discovered = applyCustomModelEntryPolicy(discovered, policy)
	discovered.BinaryVersion = version
	persistCtx := s.ctx
	if persistCtx == nil {
		persistCtx = context.Background()
	}
	if errors.Is(discoverErr, ports.ErrAgentModelDiscoverySignInRequired) {
		return s.keepCatalogUntilSignIn(persistCtx, item.Name, cached, hasCached, policy, version, generation), nil
	}
	if discoverErr != nil {
		warningCode := modelCatalogWarningCode(discoverErr)
		// Provider model IDs are credential-scoped. Reuse a cached catalog only
		// when it was produced from the same discovery inputs; otherwise a revoked
		// key or provider switch could leave invalid IDs in the picker.
		cacheMatchesInputs := hasCached && cached.BinaryVersion == version
		if cacheMatchesInputs && len(cached.Catalog.Models) > 0 {
			cached.Catalog.Stale = true
			cached.Catalog.Warning = discoverErr.Error()
			cached.Catalog.WarningCode = warningCode
			cached.Catalog.RefreshRecommended = true
			if err := s.saveFailedCatalog(persistCtx, cached, cached.Catalog, generation); err != nil {
				cached.Catalog.Warning = appendCacheWarning(cached.Catalog.Warning)
			} else if updated, ok, _ := s.cachedCatalog(persistCtx, agentID, projectID); ok {
				return updated.Catalog, nil
			}
			return cached.Catalog, nil
		}
		if len(discovered.Models) > 0 {
			discovered.Stale = true
			discovered.Warning = discoverErr.Error()
			discovered.WarningCode = warningCode
			discovered.RefreshRecommended = true
			previous := cached
			if !cacheMatchesInputs {
				previous.Catalog.LastSuccessAt = nil
				previous.Catalog.Metadata = catalogMetadata(request)
			}
			if err := s.saveFailedCatalog(persistCtx, previous, discovered, generation); err != nil {
				discovered.Warning = appendCacheWarning(discovered.Warning)
			} else if updated, ok, _ := s.cachedCatalog(persistCtx, agentID, projectID); ok {
				return updated.Catalog, nil
			}
			return discovered, nil
		}
		fallback := policy
		fallback.BinaryVersion = version
		fallback.Stale = true
		fallback.Warning = discoverErr.Error()
		fallback.WarningCode = warningCode
		fallback.RefreshRecommended = true
		// An empty catalog still carries retry history. Preserve it for the same
		// inputs so cold discovery failures eventually exhaust their retry budget.
		previous := cached
		if !cacheMatchesInputs {
			previous = decodedCatalog{Catalog: fallback, ProjectID: projectID}
			previous.Catalog.Metadata = catalogMetadata(request)
		}
		if err := s.saveFailedCatalog(persistCtx, previous, fallback, generation); err == nil {
			if updated, found, _ := s.cachedCatalog(persistCtx, agentID, projectID); found {
				return updated.Catalog, nil
			}
		}
		return fallback, nil
	}
	now := s.now().UTC()
	discovered.ValidatedAt = now
	discovered.LastSuccessAt = &now
	discovered.InputFingerprint = version
	discovered.Metadata = catalogMetadata(request)
	discovered.RefreshState = "idle"
	discovered.RefreshError = ""
	discovered.RetryAt = nil
	discovered.RefreshRecommended = false
	if err := s.saveCatalog(persistCtx, projectID, discovered, generation, 0); err != nil {
		discovered.Warning = appendCacheWarning(discovered.Warning)
	}
	return discovered, nil
}

// keepCatalogUntilSignIn handles discovery skipped because the agent is
// clearly signed out. That is not a failure: no retry budget is spent and no
// retry timer is set. A cached catalog keeps its models but loses any earlier
// failure marker, and a first load stores an idle placeholder. Both carry the
// sign-in warning, so cache-first reads show it too. The record's validation
// times are cleared so it is due for revalidation regardless of when it last
// loaded: a sign-in made outside Tallpine (for example from a terminal) is picked up
// by the next picker read or daemon start, not only by Tallpine's own auth probe.
// The refresh state stays idle because the picker shows a spinner for queued.
func (s *Service) keepCatalogUntilSignIn(ctx context.Context, agentName string, cached decodedCatalog, hasCached bool, policy ports.AgentModelCatalog, version string, generation int64) ports.AgentModelCatalog {
	catalog := cached.Catalog
	if !hasCached {
		catalog = policy
		catalog.BinaryVersion = version
		catalog.InputFingerprint = version
	}
	catalog.ValidatedAt = time.Time{}
	catalog.LastSuccessAt = nil
	catalog.Stale = false
	catalog.Warning = agentName + " is not signed in; sign in to load its models"
	catalog.WarningCode = ports.ModelCatalogWarningAuthRequired
	catalog.RefreshState = "idle"
	catalog.RefreshError = ""
	catalog.RetryAt = nil
	catalog.RefreshRecommended = false
	if err := s.saveCatalog(ctx, cached.ProjectID, catalog, generation, 0); err != nil {
		catalog.Warning = appendCacheWarning(catalog.Warning)
	}
	return catalog
}
