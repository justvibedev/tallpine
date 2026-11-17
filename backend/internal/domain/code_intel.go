package domain

import (
	"encoding/json"
	"slices"
)

// CodeIntelPrompt is the system-prompt text of a worker spawned with code
// intelligence mode (`tallpine spawn --code-intel`): navigate Go/TS symbols
// with the LSP tool, not grep. It only works together with CodeIntelDeny.
const CodeIntelPrompt = "Code navigation: for definitions, references, callers, implementations and rename impact of Go/TS symbols, use the LSP tool (load it first with ToolSearch \"select:LSP\") instead of grep.\n" +
	"Use grep only for literal text (strings, comments, config, non-code files).\n" +
	"Shell grep/rg are disabled; navigate code with LSP and Read."

// CodeIntelDeny is the Claude Code permissions.deny list of that mode: every
// shell grep spelling (rtk is the owner's rewrite hook) plus the Grep tool.
var CodeIntelDeny = []string{
	"Bash(grep *)", "Bash(egrep *)", "Bash(rg *)", "Bash(git grep *)",
	"Bash(rtk grep *)", "Bash(rtk rg *)", "Bash(ag *)", "Bash(ack *)", "Grep",
}

// CodeIntelApplies reports whether code intelligence mode changes a session:
// Claude Code workers only.
func CodeIntelApplies(kind SessionKind, harness AgentHarness) bool {
	return kind == KindWorker && harness == HarnessClaudeCode
}

// WithCodeIntelDeny adds CodeIntelDeny to a Claude settings object (the
// EnvClaudeRoleSettings JSON, "" for none) as permissions.deny, keeping the
// other keys and any deny rules already there.
func WithCodeIntelDeny(settingsJSON string) string {
	settings := map[string]json.RawMessage{}
	if settingsJSON != "" {
		if json.Unmarshal([]byte(settingsJSON), &settings) != nil {
			return settingsJSON
		}
	}
	perms := map[string]json.RawMessage{}
	if raw, ok := settings["permissions"]; ok {
		_ = json.Unmarshal(raw, &perms)
	}
	var deny []string
	if raw, ok := perms["deny"]; ok {
		_ = json.Unmarshal(raw, &deny)
	}
	for _, rule := range CodeIntelDeny {
		if !slices.Contains(deny, rule) {
			deny = append(deny, rule)
		}
	}
	perms["deny"], _ = json.Marshal(deny)
	settings["permissions"], _ = json.Marshal(perms)
	encoded, _ := json.Marshal(settings)
	return string(encoded)
}
