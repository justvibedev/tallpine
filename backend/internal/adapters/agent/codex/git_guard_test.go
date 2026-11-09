package codex

import (
	"strings"
	"testing"
)

func TestGitGuardThreadConfigRunsOnlyTheGuard(t *testing.T) {
	config := GitGuardThreadConfig()
	if config["bypass_hook_trust"] != true {
		t.Fatalf("bypass_hook_trust = %#v", config["bypass_hook_trust"])
	}
	hooks, _ := config["hooks"].(map[string]any)
	groups, _ := hooks["PreToolUse"].([]any)
	if len(hooks) != 1 || len(groups) != 1 {
		t.Fatalf("hooks = %#v", hooks)
	}
	group, _ := groups[0].(map[string]any)
	entries, _ := group["hooks"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
	entry, _ := entries[0].(map[string]any)
	command, _ := entry["command"].(string)
	if !strings.HasSuffix(command, " hooks codex git-guard") || entry["type"] != "command" {
		t.Fatalf("hook = %#v", entry)
	}
}
