package sessionmanager

import (
	"strings"
	"testing"
)

func TestContextAnalystPromptForbidsTrimmingRulesAndMessaging(t *testing.T) {
	for _, want := range []string{"Never trim, weaken, reorder or drop a behavioral rule", "Never propose deleting", "Do not message anyone", "tallpine send", "feedback or user", "At most 10"} {
		if !strings.Contains(contextAnalystPrompt, want) {
			t.Errorf("analyst prompt lacks %q", want)
		}
	}
}

func TestContextAnalystSystemPromptHasNoWorkerReportSection(t *testing.T) {
	got := buildSystemPromptText(systemPromptConfig{Role: sessionPromptRoleContextAnalyst})
	if !strings.Contains(got, "CONTEXT ANALYST") {
		t.Fatalf("analyst section missing: %s", got)
	}
	for _, bad := range []string{"tallpine report --done", "## Worker Reports", "tallpine send"} {
		if strings.Contains(strings.ReplaceAll(got, "no `tallpine send`", ""), bad) {
			t.Errorf("analyst system prompt contains %q", bad)
		}
	}
}
