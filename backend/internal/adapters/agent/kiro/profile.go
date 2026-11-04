package kiro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// kiroSteeringResources makes repo rules load: a custom agent includes steering
// only through resources. Tallpine's own rules stay in the agent prompt.
var kiroSteeringResources = []string{"file://AGENTS.md", "file://.kiro/steering/**/*.md"}

type kiroPermissionRule struct {
	Capability string   `json:"capability"`
	Match      []string `json:"match,omitempty"`
	Effect     string   `json:"effect"`
}

var kiroWorkspaceGlob = []string{"**"}

// kiroDevShellCommands is the shell allow-list for auto mode: specific VCS, gh and report
// verbs and fixed build/test commands only (never a bare `<tool> *`, which would
// admit `git -c`, `gh api -X DELETE`, `go test -exec`). Interpreters and package runners (python, npx, npm,
// go run, make <target>) and file-manipulation commands (cp, mv, cat...) are left
// out because they run arbitrary code or reach outside the workspace scoping that
// fs_read/fs_write apply; those still ask.
var kiroDevShellCommands = []string{
	// Kiro v3 splits compound commands and checks each part on its own, so the
	// `cd <worktree> &&` prefix of a chained command needs its own allow rule.
	// cd only changes directory; the other parts still need to match.
	"cd *",
	"git status", "git status *", "git diff *", "git log *", "git show *", "git add *", "git commit *", "git push *",
	"gh pr view *", "gh pr create *", "gh pr checks *",
	"tallpine report *",
	"go build ./...", "go test ./...", "go vet ./...", "cargo build", "cargo test", "make test", "npm test",
}

// kiroPermissionRules maps a Tallpine permission mode onto the documented v3
// agent-profile `permissions: {rules: [...]}` shape. Anything not allowed here
// falls through to Kiro's default (ask).
//
//	default      -> read the workspace
//	accept-edits -> read + write the workspace
//	auto         -> accept-edits plus a dev shell allow-list
//	bypass       -> allow everything (also launched with --trust-all-tools)
func kiroPermissionRules(mode ports.PermissionMode) []kiroPermissionRule {
	read := kiroPermissionRule{Capability: "fs_read", Match: kiroWorkspaceGlob, Effect: "allow"}
	write := kiroPermissionRule{Capability: "fs_write", Match: kiroWorkspaceGlob, Effect: "allow"}
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return []kiroPermissionRule{read, write}
	case ports.PermissionModeAuto:
		return []kiroPermissionRule{read, write, {Capability: "shell", Match: kiroDevShellCommands, Effect: "allow"}}
	case ports.PermissionModeBypassPermissions:
		return []kiroPermissionRule{{Capability: "all", Effect: "allow"}}
	}
	return []kiroPermissionRule{read}
}

func setKiroAgentDefaults(topLevel map[string]json.RawMessage, systemPromptFile string, agentConfig ports.AgentConfig) error {
	// V3 skips a profile that has allowedTools and no permissions block, and
	// ignores toolsSettings, so drop both legacy fields.
	delete(topLevel, "allowedTools")
	delete(topLevel, "toolsSettings")

	defaults := map[string]any{
		"name":           kiroAgentName,
		"description":    kiroAgentDescription,
		"prompt":         nil,
		"mcpServers":     map[string]any{},
		"tools":          []string{"*"},
		"toolAliases":    map[string]any{},
		"resources":      kiroSteeringResources,
		"includeMcpJson": true,
		// Managed: follows the session's permission mode on every install.
		"permissions": map[string]any{"rules": kiroPermissionRules(agentConfig.Permissions)},
	}
	if promptFile := strings.TrimSpace(systemPromptFile); promptFile != "" {
		// kiro-cli 2.37 v3 rejects absolute file:// prompt URIs (and resolves
		// relative ones against .kiro/agents), so the text is inlined.
		body, err := os.ReadFile(filepath.Clean(promptFile))
		if err != nil {
			return fmt.Errorf("read system prompt file: %w", err)
		}
		defaults["prompt"] = string(body)
	}
	if model := strings.TrimSpace(agentConfig.Model); model != "" {
		defaults["model"] = model
	} else {
		delete(topLevel, "model")
	}
	for key, value := range defaults {
		managedKey := key == "name" || key == "prompt" || key == "model" || key == "permissions" || key == "resources"
		if !managedKey {
			if _, ok := topLevel[key]; ok {
				continue
			}
		}
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode agent %s: %w", key, err)
		}
		topLevel[key] = data
	}
	return nil
}
