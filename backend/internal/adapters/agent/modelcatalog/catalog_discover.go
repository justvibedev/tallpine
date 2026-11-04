package modelcatalog

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/pkg/agentcreds"
)

// discoverACPOptionCatalog turns the model select an ACP session advertises into
// the shared catalog shape.
func discoverACPOptionCatalog(
	ctx context.Context,
	request ports.AgentModelDiscoveryRequest,
	list ACPOptionListFunc,
) (ports.AgentModelCatalog, error) {
	base := Base(request.AgentID)
	options, err := list(ctx, request)
	if err != nil {
		return base, fmt.Errorf("%s ACP model discovery: %w", request.AgentID, err)
	}
	var models []ports.AgentModelInfo
	for _, option := range options {
		if option.Type != ports.ChatConfigOptionSelect || (option.Category != "model" && option.ID != "model") {
			continue
		}
		for _, choice := range option.Choices {
			id := strings.TrimSpace(choice.Value)
			if id == "" {
				continue
			}
			label := strings.TrimSpace(choice.Name)
			if label == "" {
				label = id
			}
			models = append(models, ports.AgentModelInfo{
				ID: id, Label: label, Provider: strings.TrimSpace(choice.Group),
				IsDefault: id == strings.TrimSpace(option.Current.Select),
			})
		}
	}
	if models = normalize(models); len(models) == 0 {
		return base, fmt.Errorf("%s ACP model discovery returned no models", request.AgentID)
	}
	base.Models = models
	base.Source = "acp"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}

// discoverClaudeCatalog reports only provider-discovered models. Errors leave
// the shared service to retain the matching cached catalog or allow manual
// entry when no successful catalog has been saved yet.
func discoverClaudeCatalog(
	ctx context.Context,
	request ports.AgentModelDiscoveryRequest,
	list ClaudeModelListFunc,
) (ports.AgentModelCatalog, error) {
	base := Manual(request.AgentID)
	if list == nil {
		return base, errors.New("claude-code model discovery is unavailable")
	}
	models, err := list(ctx, request)
	if err != nil {
		return base, fmt.Errorf("claude-code model discovery: %w", err)
	}
	normalized := normalize(models)
	if len(normalized) == 0 {
		return base, errors.New("claude-code model discovery returned no models")
	}
	settings := agentcreds.ResolveClaudeSettings(ctx, request.WorkingDir, request.Env, agentcreds.ResolveOptions{})
	// An explicitly configured alias may accompany the provider's concrete
	// models; use the discovered catalog to resolve its display version.
	base.Models = LabelClaudeAliasVersions(applyClaudeConfiguredDefault(normalized, settings.Model), normalized)
	base.SelectionMode = ports.ModelSelectionCatalog
	base.Source = "provider"
	return base, nil
}

func applyClaudeConfiguredDefault(models []ports.AgentModelInfo, configured string) []ports.AgentModelInfo {
	if configured == "" {
		return models
	}
	matched := false
	for i := range models {
		models[i].IsDefault = strings.EqualFold(models[i].ID, configured)
		matched = matched || models[i].IsDefault
	}
	if !matched {
		// Preserve the user's explicit default, including custom aliases and
		// pinned snapshots. Its display version comes from the provider list.
		models = append(models, ports.AgentModelInfo{ID: configured, Label: configured, IsDefault: true})
	}
	return models
}

// CatalogFingerprint returns a stable fingerprint of the discovery inputs: the
// installed agent binary plus the configuration this adapter reads to build the
// catalog. Folding configuration in is what lets a settings edit invalidate a
// cached catalog, since the binary alone does not change when settings do.
func (d Discoverer) CatalogFingerprint(ctx context.Context, request ports.AgentModelDiscoveryRequest) string {
	fingerprint := CatalogFingerprint(ctx, request.AgentID, request.Binary, request.WorkingDir, request.Env)
	if request.AgentID != "claude-code" || d.ClaudeFingerprint == nil {
		return fingerprint
	}
	extra := d.ClaudeFingerprint(ctx, request)
	hash := sha256.Sum256([]byte(fingerprint + "\x00" + extra))
	return fmt.Sprintf("%x", hash[:8])
}

// Manual returns the manual-entry fallback catalog for an agent.
func (Discoverer) Manual(agentID string) ports.AgentModelCatalog { return Manual(agentID) }

// discoverCommandCatalog combines native output and local provider metadata.
// Credential-scoped cloud discovery excludes the device-local catalogs.
func discoverCommandCatalog(ctx context.Context, agentID, binary, workingDir string, env map[string]string) (ports.AgentModelCatalog, error) {
	base := Base(agentID)
	spec, ok := commandSpecs[agentID]
	if !ok {
		return base, nil
	}
	if strings.TrimSpace(binary) == "" {
		return base, errors.New("agent binary is not installed")
	}
	if spec.signIn != nil {
		if err := spec.signIn.check(ctx, agentID, binary, workingDir, env); err != nil {
			return base, err
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := modelCommand(runCtx, binary, spec.args, workingDir, env)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return base, modelDiscoveryError(runCtx, agentID, err, output)
	}
	models, err := spec.parser(output)
	if err != nil {
		return base, fmt.Errorf("%s model discovery: %w", agentID, err)
	}
	base.Providers = reportedJSONProviders(output)
	models = normalize(models)
	if len(models) == 0 && len(base.Providers) == 0 {
		return base, fmt.Errorf("%s model discovery returned no models", agentID)
	}
	base.Models = models
	base.Source = "cli"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}

func discoverCodexCatalog(ctx context.Context, request ports.AgentModelDiscoveryRequest, list CodexModelListFunc) (ports.AgentModelCatalog, error) {
	base := Base(request.AgentID)
	if list == nil {
		return base, errors.New("codex app-server model discovery is unavailable")
	}
	models, err := list(ctx, request)
	if err != nil {
		return base, fmt.Errorf("codex model discovery: %w", err)
	}
	normalized := make([]ports.AgentModelInfo, 0, len(models))
	for _, item := range models {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		label := strings.TrimSpace(item.DisplayName)
		if label == "" {
			label = id
		}
		provider := ""
		if prefix, _, ok := strings.Cut(id, "/"); ok {
			provider = prefix
		}
		normalized = append(normalized, ports.AgentModelInfo{
			ID: id, Label: label, Provider: provider, IsDefault: item.Default,
			Efforts: append([]string(nil), item.Efforts...), DefaultEffort: item.DefaultEffort,
			SupportsFast: item.FastTier != "",
			Inputs:       modelInputs(item.Inputs),
		})
	}
	if len(normalized) == 0 {
		return base, errors.New("codex model discovery returned no models")
	}
	base.Models = sortNewestFirst(normalize(normalized))
	base.Source = "cli"
	base.FetchedAt = time.Now().UTC()
	return base, nil
}
