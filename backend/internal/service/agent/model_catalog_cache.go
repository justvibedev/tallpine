package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/backoff"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// modelCatalogWarningCode classifies a discovery failure that clients can
// resolve with the agent's own login.
func modelCatalogWarningCode(err error) string {
	switch {
	case errors.Is(err, ports.ErrAgentModelDiscoveryCredentialExpired):
		return ports.ModelCatalogWarningAuthExpired
	case errors.Is(err, ports.ErrAgentModelDiscoveryCredentialRejected),
		errors.Is(err, ports.ErrAgentModelDiscoverySignInRequired):
		return ports.ModelCatalogWarningAuthRequired
	default:
		return ""
	}
}

func applyCustomModelEntryPolicy(catalog, policy ports.AgentModelCatalog) ports.AgentModelCatalog {
	entryMode := policy.CustomModelEntry
	if entryMode == "" {
		if policy.AllowCustom {
			entryMode = ports.CustomModelEntryDirect
		} else {
			entryMode = ports.CustomModelEntryNone
		}
	}
	catalog.CustomModelEntry = entryMode
	catalog.AllowCustom = entryMode == ports.CustomModelEntryDirect
	return catalog
}

func appendCacheWarning(current string) string {
	const next = "Models loaded, but Tallpine could not update the model cache."
	if current == "" {
		return next
	}
	return current + " " + next
}

type decodedCatalog struct {
	Catalog       ports.AgentModelCatalog
	ProjectID     string
	BinaryVersion string
	LastSuccessAt time.Time
	RefreshState  string
	RefreshError  string
	RetryCount    int64
	RetryAt       time.Time
	Generation    int64
}

func (s *Service) cachedCatalog(ctx context.Context, agentID, projectID string) (decodedCatalog, bool, error) {
	if s.cache == nil {
		return decodedCatalog{}, false, nil
	}
	record, ok, err := s.cache.GetAgentModelCatalog(ctx, agentID, projectID)
	if err != nil || !ok {
		return decodedCatalog{}, ok, err
	}
	var catalog ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &catalog); err != nil {
		return decodedCatalog{}, false, fmt.Errorf("decode cached model catalog for %s: %w", agentID, err)
	}
	if catalog.Models == nil {
		catalog.Models = []ports.AgentModelInfo{}
	}
	if catalog.LastSuccessAt == nil || catalog.LastSuccessAt.IsZero() {
		lastSuccess := record.LastSuccessAt
		if lastSuccess.IsZero() {
			lastSuccess = catalog.ValidatedAt
		}
		if !lastSuccess.IsZero() {
			catalog.LastSuccessAt = &lastSuccess
		}
	}
	catalog.RefreshState = record.RefreshState
	catalog.RefreshError = record.RefreshError
	catalog.RetryAt = modelCatalogRetryAt(record.RetryAt)
	return decodedCatalog{Catalog: catalog, ProjectID: record.ProjectID, BinaryVersion: record.BinaryVersion, LastSuccessAt: catalogLastSuccess(catalog), RefreshState: record.RefreshState, RefreshError: record.RefreshError, RetryCount: record.RetryCount, RetryAt: record.RetryAt, Generation: record.Generation}, true, nil
}

func catalogLastSuccess(catalog ports.AgentModelCatalog) time.Time {
	if catalog.LastSuccessAt == nil {
		return time.Time{}
	}
	return *catalog.LastSuccessAt
}

func (s *Service) saveCatalog(ctx context.Context, projectID string, catalog ports.AgentModelCatalog, generation, retryCount int64) error {
	if s.cache == nil {
		return nil
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		return fmt.Errorf("encode model catalog for %s: %w", catalog.AgentID, err)
	}
	metadata, _ := json.Marshal(catalog.Metadata)
	return s.cache.UpsertAgentModelCatalog(ctx, ports.CachedAgentModelCatalog{
		AgentID:          catalog.AgentID,
		ProjectID:        projectID,
		BinaryVersion:    catalog.BinaryVersion,
		CatalogJSON:      string(data),
		Source:           catalog.Source,
		FetchedAt:        catalog.FetchedAt,
		MetadataJSON:     string(metadata),
		InputFingerprint: catalog.InputFingerprint,
		LastSuccessAt:    catalogLastSuccess(catalog),
		RefreshState:     catalog.RefreshState,
		RefreshError:     catalog.RefreshError,
		RetryCount:       retryCount,
		RetryAt:          catalogRetryTime(catalog.RetryAt),
		Generation:       generation,
	})
}

func catalogMetadata(request ports.AgentModelDiscoveryRequest) map[string]string {
	metadata := map[string]string{"scope": "global"}
	if request.WorkingDir != "" {
		metadata["scope"] = "project"
	}
	if request.Binary != "" {
		metadata["binary"] = request.Binary
	}
	return metadata
}

func (s *Service) persistCatalogState(ctx context.Context, cached decodedCatalog, hasCached bool, state, message string, retryAt time.Time, generation int64) error {
	if !hasCached {
		return nil
	}
	catalog := cached.Catalog
	catalog.RefreshState = state
	catalog.RefreshError = message
	catalog.RetryAt = modelCatalogRetryAt(retryAt)
	return s.saveCatalog(ctx, cached.ProjectID, catalog, generation, cached.RetryCount)
}

func (s *Service) saveFailedCatalog(ctx context.Context, previous decodedCatalog, catalog ports.AgentModelCatalog, generation int64) error {
	retryCount := previous.RetryCount + 1
	catalog.LastSuccessAt = previous.Catalog.LastSuccessAt
	catalog.RefreshState = "error"
	catalog.RefreshError = catalog.Warning
	catalog.InputFingerprint = catalog.BinaryVersion
	catalog.Metadata = previous.Catalog.Metadata
	catalog.RetryAt = nil
	catalog.RefreshRecommended = retryCount <= int64(modelCatalogMaxRetries)
	var retryDelay time.Duration
	if retryCount <= int64(modelCatalogMaxRetries) {
		retryDelay = backoff.Table(modelCatalogRetryDelays[:], int(retryCount))
		retryAt := s.now().Add(retryDelay).UTC()
		catalog.RetryAt = &retryAt
	}
	if err := s.saveCatalog(ctx, previous.ProjectID, catalog, generation, retryCount); err != nil {
		return err
	}
	if catalog.RetryAt != nil && s.cache != nil {
		retryAt := *catalog.RetryAt
		time.AfterFunc(retryDelay, func() {
			if s.ctx.Err() != nil {
				return
			}
			record, ok, err := s.cache.GetAgentModelCatalog(s.ctx, catalog.AgentID, previous.ProjectID)
			if err != nil || !ok || record.Generation != generation || record.RefreshState != "error" || !record.RetryAt.Equal(retryAt) {
				return
			}
			_, _ = s.revalidateModels(s.ctx, catalog.AgentID, previous.ProjectID)
		})
	}
	return nil
}

func modelCatalogRetriesExhausted(cached decodedCatalog) bool {
	return cached.RefreshState == "error" && cached.RetryCount > int64(modelCatalogMaxRetries)
}

func modelCatalogRetryAt(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func catalogRetryTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return value.UTC()
}
