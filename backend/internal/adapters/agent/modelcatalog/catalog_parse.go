package modelcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func parseJSONModels(output []byte) ([]ports.AgentModelInfo, error) {
	var root any
	if err := json.Unmarshal(output, &root); err != nil {
		return nil, err
	}
	var models []ports.AgentModelInfo
	var walk func(any, string)
	walk = func(value any, inheritedProvider string) {
		switch node := value.(type) {
		case []any:
			for _, item := range node {
				walk(item, inheritedProvider)
			}
		case map[string]any:
			provider := firstString(node, "provider", "providerId", "provider_id")
			if provider == "" {
				provider = inheritedProvider
			}
			if _, container := node["models"]; container && provider == "" {
				provider = firstString(node, "id")
			}
			id := firstString(node, "selector", "modelId", "model_id", "model_uid", "slug", "model")
			if id == "" {
				if _, isProviderContainer := node["models"]; !isProviderContainer {
					id = firstString(node, "id")
				}
			}
			if id != "" {
				label := firstString(node, "displayName", "display_name", "model_name", "label", "name")
				if label == "" {
					label = id
				}
				models = append(models, ports.AgentModelInfo{
					ID:            id,
					Label:         label,
					Provider:      provider,
					IsDefault:     firstBool(node, "isDefault", "is_default", "default"),
					ContextWindow: catalogContextWindow(node),
					Inputs:        catalogInputs(node),
				})
			}
			for key, child := range node {
				if key == "models" {
					if modelMap, ok := child.(map[string]any); ok {
						for alias, item := range modelMap {
							if modelNode, ok := item.(map[string]any); ok && strings.TrimSpace(alias) != "" && looksLikeModelAliasRecord(modelNode) {
								modelProvider := firstString(modelNode, "provider", "providerId", "provider_id")
								if modelProvider == "" {
									modelProvider = provider
								}
								label := firstString(modelNode, "displayName", "display_name", "model_name", "label", "name")
								if label == "" {
									label = alias
								}
								models = append(models, ports.AgentModelInfo{
									ID:            strings.TrimSpace(alias),
									Label:         label,
									Provider:      modelProvider,
									IsDefault:     firstBool(modelNode, "isDefault", "is_default", "default"),
									ContextWindow: catalogContextWindow(modelNode),
									Inputs:        catalogInputs(modelNode),
								})
								continue
							}
							walk(item, provider)
						}
						continue
					}
				}
				walk(child, provider)
			}
		}
	}
	walk(root, "")
	return normalize(models), nil
}

func looksLikeModelAliasRecord(node map[string]any) bool {
	if _, isModelContainer := node["models"]; isModelContainer {
		return false
	}
	return firstString(node,
		"modelId", "model_id", "model_uid", "slug", "model",
		"provider", "providerId", "provider_id",
	) != ""
}

func firstString(node map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := node[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstBool(node map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := node[key].(bool); ok {
			return value
		}
	}
	return false
}

func normalize(models []ports.AgentModelInfo) []ports.AgentModelInfo {
	byID := make(map[string]ports.AgentModelInfo, len(models))
	for _, item := range models {
		item.ID = strings.TrimSpace(item.ID)
		if item.ID == "" {
			continue
		}
		if strings.TrimSpace(item.Label) == "" {
			item.Label = item.ID
		}
		if strings.HasSuffix(strings.ToLower(item.Label), " (default)") {
			item.Label = strings.TrimSpace(item.Label[:len(item.Label)-len(" (default)")])
			item.IsDefault = true
		} else if strings.EqualFold(item.Label, "default") || strings.EqualFold(item.Label, "default (recommended)") {
			item.Label = item.ID
			item.IsDefault = true
		}
		if previous, ok := byID[item.ID]; ok {
			if previous.Label == previous.ID && item.Label != item.ID {
				previous.Label = item.Label
			}
			if previous.Provider == "" {
				previous.Provider = item.Provider
			}
			if previous.ContextWindow == 0 {
				previous.ContextWindow = item.ContextWindow
			}
			if len(previous.Inputs) == 0 {
				previous.Inputs = item.Inputs
			}
			if len(previous.Efforts) == 0 {
				previous.Efforts = item.Efforts
			}
			previous.IsDefault = previous.IsDefault || item.IsDefault
			byID[item.ID] = previous
			continue
		}
		byID[item.ID] = item
	}
	out := make([]ports.AgentModelInfo, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		left, right := strings.ToLower(out[i].Label), strings.ToLower(out[j].Label)
		if left != right {
			return left < right
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Preserve explicit JSON provider containers even when their model array is empty.
func reportedJSONProviders(raw []byte) []string {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	found := map[string]bool{}
	var walk func(any, bool)
	walk = func(value any, inProviders bool) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child, inProviders)
			}
		case map[string]any:
			_, hasModels := node["models"]
			if hasModels || inProviders {
				if id := firstString(node, "provider", "providerId", "provider_id", "id"); id != "" {
					found[id] = true
				}
			}
			for key, child := range node {
				walk(child, key == "providers")
			}
		}
	}
	walk(root, false)
	providers := make([]string, 0, len(found))
	for id := range found {
		providers = append(providers, id)
	}
	sort.Strings(providers)
	return providers
}

// modelConfigReadLimit bounds a provider credential file read for the Claude
// catalog fingerprint.
const modelConfigReadLimit = 2 << 20

func readModelConfig(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // path comes from the user's Claude provider settings
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, modelConfigReadLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > modelConfigReadLimit {
		return nil, fmt.Errorf("configuration exceeds %d bytes", modelConfigReadLimit)
	}
	return raw, nil
}
