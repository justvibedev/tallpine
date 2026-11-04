package domain

import (
	"fmt"
	"strings"
)

// Shipped model-policy defaults.
const (
	// DefaultCodexModel is the model Codex workers get when none is requested.
	DefaultCodexModel = "gpt-6.1-sol"
	// EnvPromptCacheTTL is the claude-code prompt cache TTL variable.
	EnvPromptCacheTTL = "CLAUDE_CODE_PROMPT_CACHE_TTL"
	// RoleReviewer is the PromptCacheTTL key for native reviews and review-only
	// (artifact-output) workers.
	RoleReviewer = "reviewer"
)

// ShippedModelDenylist is refused unless the project's models.allowlist names it.
var ShippedModelDenylist = []string{"gpt-6-astra"}

// ModelPolicy is the configurable model fallback and denylist.
type ModelPolicy struct {
	// CodexDefault is the Codex model used when a spawn names none. Empty uses DefaultCodexModel.
	CodexDefault string `json:"codexDefault,omitempty"`
	// Denylist adds models to ShippedModelDenylist.
	Denylist []string `json:"denylist,omitempty"`
	// Allowlist exempts models from the denylist.
	Allowlist []string `json:"allowlist,omitempty"`
}

// ResolveModel applies the Codex default to an empty model and refuses a
// denylisted one that is not allowlisted. The error names the config key.
func (c ProjectConfig) ResolveModel(harness AgentHarness, model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		if harness == HarnessCodex {
			if d := strings.TrimSpace(c.Models.CodexDefault); d != "" {
				return d, nil
			}
			return DefaultCodexModel, nil
		}
		return "", nil
	}
	if err := c.CheckModel(model); err != nil {
		return "", err
	}
	return model, nil
}

// ModelDeniedError reports a model the project's policy refuses.
type ModelDeniedError struct{ Model string }

func (e *ModelDeniedError) Error() string {
	return fmt.Sprintf("model %q is denied by policy; add it to the project config key models.allowlist to use it", e.Model)
}

// CheckModel refuses a denylisted model that is not allowlisted, with a
// *ModelDeniedError. Spawn (ResolveModel), reviewers and the in-chat switch all
// go through it, so there is one answer to "may this project run this model".
func (c ProjectConfig) CheckModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	denied := false
	for _, d := range append(append([]string{}, ShippedModelDenylist...), c.Models.Denylist...) {
		if strings.EqualFold(strings.TrimSpace(d), model) {
			denied = true
		}
	}
	for _, a := range c.Models.Allowlist {
		if strings.EqualFold(strings.TrimSpace(a), model) {
			denied = false
		}
	}
	if denied {
		return &ModelDeniedError{Model: model}
	}
	return nil
}
