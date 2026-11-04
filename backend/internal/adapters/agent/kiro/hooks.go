package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/hookutil"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	// Kiro reads hooks from a workspace-local agent configuration file at
	// .kiro/agents/<name>.json. Tallpine installs its hooks into a dedicated agent
	// file so it never clobbers a user's own agents.
	// See https://kiro.dev/docs/cli/hooks/ and
	// https://kiro.dev/docs/cli/custom-agents/configuration-reference#hooks-field
	kiroHooksDirName  = ".kiro"
	kiroAgentsDirName = "agents"
	kiroAgentFileName = brand.CLIName + ".json"

	// Kiro v3 ignores the "hooks" array of an agent profile when launched with
	// --agent (kiro.log: hooks.reload.loaded {read:0,registered:0}) and loads
	// standalone files from .kiro/hooks/ instead (verified live on kiro-cli
	// 2.37.0). Tallpine's managed hooks therefore live in
	// .kiro/hooks/tallpine.json; the agent file keeps only user hooks.
	kiroHooksSubDirName = "hooks"
	kiroHooksFileName   = brand.CLIName + ".json"
	kiroHooksVersion    = "v1"

	// kiroHookCommandPrefix identifies the hook commands Tallpine owns, so install
	// replaces and uninstall removes Tallpine entries by prefix.
	kiroHookCommandPrefix = "tallpine hooks kiro "

	// kiroHookNamePrefix names the entries Tallpine owns in the flat hooks array.
	kiroHookNamePrefix = brand.CLIName + "-"

	kiroAgentName        = brand.CLIName
	kiroAgentDescription = "Tallpine session instructions"
)

// kiroHookFile is the on-disk shape of .kiro/agents/tallpine.json as far as
// hooks go. Kiro v3 hooks are a flat array of {name, trigger, action}.
type kiroHookFile struct {
	Name   string          `json:"name"`
	Prompt *string         `json:"prompt"`
	Hooks  []kiroHookEntry `json:"hooks"`
}

type kiroHookAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type kiroHookEntry struct {
	Name    string         `json:"name"`
	Trigger string         `json:"trigger"`
	Matcher string         `json:"matcher,omitempty"`
	Action  kiroHookAction `json:"action"`
}

// kiroHookSpec describes one hook Tallpine installs, defined in code.
type kiroHookSpec struct {
	// Event is the native Kiro v3 trigger (PascalCase).
	Event string
	// Command is the Tallpine hook command line.
	Command string
}

// kiroManagedHooks is the source of truth for the hooks Tallpine installs. Native
// triggers map onto Tallpine hook sub-command names (the trailing word) so the CLI
// hook dispatcher routes them to DeriveActivityState.
//
// PreToolUse maps to pre-tool-use (active), NOT permission-request: it fires for
// every tool invocation including auto-approved ones, so treating it as
// waiting_input pins ordinary tool use to sticky Input needed. Genuine approval
// dialogs carry no hook and are observed via DetectTerminalActivity.
var kiroManagedHooks = []kiroHookSpec{
	{Event: "SessionStart", Command: kiroHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: kiroHookCommandPrefix + "user-prompt-submit"},
	{Event: "PreToolUse", Command: kiroHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: kiroHookCommandPrefix + "post-tool-use"},
	{Event: "Stop", Command: kiroHookCommandPrefix + "stop"},
	{Event: "SessionEnd", Command: kiroHookCommandPrefix + "session-end"},
}

// kiroLegacyTriggers maps the v2 camelCase event keys of the legacy object form
// onto v3 triggers, so user entries in an old-style file survive conversion.
var kiroLegacyTriggers = map[string]string{
	"agentSpawn":       "SessionStart",
	"userPromptSubmit": "UserPromptSubmit",
	"preToolUse":       "PreToolUse",
	"postToolUse":      "PostToolUse",
	"stop":             "Stop",
}

// GetAgentHooks installs Tallpine's Kiro hooks into the worktree-local
// .kiro/agents/tallpine.json file as flat-array entries. Existing user entries are
// preserved; Tallpine entries are replaced, so repeated installs do not duplicate.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.WorkspacePath) == "" {
		return errors.New("kiro.GetAgentHooks: WorkspacePath is required")
	}
	if strings.TrimSpace(cfg.SystemPrompt) != "" && strings.TrimSpace(cfg.SystemPromptFile) == "" {
		return fmt.Errorf("kiro.GetAgentHooks: %w", errors.New("kiro: system prompt file required to build agent config"))
	}

	hooksPath := kiroAgentPath(cfg.WorkspacePath)
	topLevel, userHooks, err := readKiroHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("kiro.GetAgentHooks: %w", err)
	}
	managed := make([]kiroHookEntry, 0, len(kiroManagedHooks))
	for _, spec := range kiroManagedHooks {
		managed = append(managed, kiroHookEntry{
			Name:    kiroHookNamePrefix + strings.TrimPrefix(spec.Command, kiroHookCommandPrefix),
			Trigger: spec.Event,
			Action:  kiroHookAction{Type: "command", Command: spec.Command},
		})
	}
	if err := writeKiroHooks(hooksPath, topLevel, userHooks, cfg.SystemPromptFile, cfg.Config); err != nil {
		return fmt.Errorf("kiro.GetAgentHooks: %w", err)
	}
	standalone := kiroStandaloneHooksPath(cfg.WorkspacePath)
	if err := writeKiroStandaloneHooks(standalone, managed); err != nil {
		return fmt.Errorf("kiro.GetAgentHooks: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(standalone), kiroHooksFileName); err != nil {
		return fmt.Errorf("kiro.GetAgentHooks: gitignore: %w", err)
	}
	if err := hookutil.EnsureWorkspaceGitignore(filepath.Dir(hooksPath), kiroAgentFileName); err != nil {
		return fmt.Errorf("kiro.GetAgentHooks: gitignore: %w", err)
	}
	return nil
}

// UninstallHooks removes Tallpine's Kiro hooks from the workspace-local agent
// file, leaving user-defined hooks untouched. A missing file is a no-op.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return errors.New("kiro.UninstallHooks: workspacePath is required")
	}
	if err := os.Remove(kiroStandaloneHooksPath(workspacePath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("kiro.UninstallHooks: %w", err)
	}
	hooksPath := kiroAgentPath(workspacePath)
	if _, err := os.Stat(hooksPath); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	topLevel, userHooks, err := readKiroHooks(hooksPath)
	if err != nil {
		return fmt.Errorf("kiro.UninstallHooks: %w", err)
	}
	if err := writeKiroHooks(hooksPath, topLevel, userHooks, "", ports.AgentConfig{}); err != nil {
		return fmt.Errorf("kiro.UninstallHooks: %w", err)
	}
	return nil
}

// AreHooksInstalled reports whether any Tallpine Kiro hook is present in the
// workspace-local agent file. A missing file means none are installed.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strings.TrimSpace(workspacePath) == "" {
		return false, errors.New("kiro.AreHooksInstalled: workspacePath is required")
	}
	if sdata, serr := os.ReadFile(kiroStandaloneHooksPath(workspacePath)); serr == nil { //nolint:gosec // caller-owned workspace dir
		var sf struct {
			Hooks []json.RawMessage `json:"hooks"`
		}
		if json.Unmarshal(sdata, &sf) == nil {
			for _, raw := range sf.Hooks {
				if isKiroManagedRaw(raw) {
					return true, nil
				}
			}
		}
	}
	hooksPath := kiroAgentPath(workspacePath)
	data, err := os.ReadFile(hooksPath) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("kiro.AreHooksInstalled: read %s: %w", hooksPath, err)
	}
	var top map[string]json.RawMessage
	if strings.TrimSpace(string(data)) == "" {
		return false, nil
	}
	if err := json.Unmarshal(data, &top); err != nil {
		return false, fmt.Errorf("kiro.AreHooksInstalled: parse %s: %w", hooksPath, err)
	}
	for _, raw := range decodeKiroHooksArray(top["hooks"]) {
		if isKiroManagedRaw(raw) {
			return true, nil
		}
	}
	return false, nil
}

func kiroAgentPath(workspacePath string) string {
	return filepath.Join(workspacePath, kiroHooksDirName, kiroAgentsDirName, kiroAgentFileName)
}

func kiroStandaloneHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, kiroHooksDirName, kiroHooksSubDirName, kiroHooksFileName)
}

// writeKiroStandaloneHooks writes the v3 standalone hook document.
func writeKiroStandaloneHooks(path string, hooks []kiroHookEntry) error {
	doc := struct {
		Version string          `json:"version"`
		Hooks   []kiroHookEntry `json:"hooks"`
	}{Version: kiroHooksVersion, Hooks: hooks}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create hook dir: %w", err)
	}
	if err := hookutil.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func isKiroManagedHook(command string) bool {
	return brand.IsHookCommand(command, kiroHookCommandPrefix)
}

func isKiroManagedRaw(raw json.RawMessage) bool {
	var probe struct {
		Action struct {
			Command string `json:"command"`
		} `json:"action"`
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return false
	}
	return isKiroManagedHook(probe.Action.Command) || isKiroManagedHook(probe.Command)
}

// decodeKiroHooksArray returns the entries of a flat hooks array; anything else
// (absent, legacy object) yields none.
func decodeKiroHooksArray(raw json.RawMessage) []json.RawMessage {
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	return arr
}

// readKiroHooks loads the agent file into a top-level raw map plus the user's hook
// entries (Tallpine-owned entries dropped). A legacy object-form "hooks" value is
// converted: user entries become flat-array entries with v3 triggers. A missing or
// empty file yields empty results.
func readKiroHooks(hooksPath string) (topLevel map[string]json.RawMessage, userHooks []json.RawMessage, err error) {
	topLevel = map[string]json.RawMessage{}
	data, err := os.ReadFile(hooksPath) //nolint:gosec // path built from caller-owned workspace dir
	if errors.Is(err, os.ErrNotExist) {
		return topLevel, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", hooksPath, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return topLevel, nil, nil
	}
	if err := json.Unmarshal(data, &topLevel); err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", hooksPath, err)
	}
	raw, ok := topLevel["hooks"]
	if !ok || strings.TrimSpace(string(raw)) == "null" {
		return topLevel, nil, nil
	}
	if trimmed := strings.TrimSpace(string(raw)); strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, nil, fmt.Errorf("parse hooks in %s: %w", hooksPath, err)
		}
		for _, entry := range arr {
			if !isKiroManagedRaw(entry) {
				userHooks = append(userHooks, entry)
			}
		}
		return topLevel, userHooks, nil
	}
	legacy := map[string][]map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, nil, fmt.Errorf("parse hooks in %s: %w", hooksPath, err)
	}
	events := make([]string, 0, len(legacy))
	for event := range legacy {
		events = append(events, event)
	}
	sort.Strings(events)
	for _, event := range events {
		trigger := kiroLegacyTriggers[event]
		if trigger == "" {
			trigger = event
		}
		for i, e := range legacy[event] {
			var command string
			_ = json.Unmarshal(e["command"], &command)
			if isKiroManagedHook(command) {
				continue
			}
			// Carry every other field (matcher, timeout_ms, ...) across unchanged.
			entry := map[string]any{}
			for k, v := range e {
				if k != "command" {
					entry[k] = v
				}
			}
			entry["name"] = fmt.Sprintf("user-%s-%d", event, i)
			entry["trigger"] = trigger
			entry["action"] = kiroHookAction{Type: "command", Command: command}
			converted, err := json.Marshal(entry)
			if err != nil {
				return nil, nil, fmt.Errorf("convert %s hook: %w", event, err)
			}
			userHooks = append(userHooks, converted)
		}
	}
	return topLevel, userHooks, nil
}

// writeKiroHooks writes the agent file with the given hooks array. An empty array
// drops the "hooks" key.
func writeKiroHooks(hooksPath string, topLevel map[string]json.RawMessage, hooks []json.RawMessage, systemPromptFile string, agentConfig ports.AgentConfig) error {
	if err := setKiroAgentDefaults(topLevel, systemPromptFile, agentConfig); err != nil {
		return err
	}
	if len(hooks) == 0 {
		delete(topLevel, "hooks")
	} else {
		hooksJSON, err := json.Marshal(hooks)
		if err != nil {
			return fmt.Errorf("encode hooks: %w", err)
		}
		topLevel["hooks"] = hooksJSON
	}
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o750); err != nil {
		return fmt.Errorf("create hook dir: %w", err)
	}
	data, err := json.MarshalIndent(topLevel, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", hooksPath, err)
	}
	data = append(data, '\n')
	if err := hookutil.AtomicWriteFile(hooksPath, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", hooksPath, err)
	}
	return nil
}
