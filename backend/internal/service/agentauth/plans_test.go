package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func TestPlansMatchAuthenticationMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id, title, executable, guidance, docs, terminalInput string
		action                                               Action
		argv                                                 []string
	}{
		{"claude-code", "Log in to Claude Code", "claude", "Choose Claude subscription, Anthropic Console, or SSO", "https://code.claude.com/docs/en/installation", "", ActionLogin, []string{"claude", "auth", "login"}},
		{"codex", "Log in to Codex", "codex", "Choose ChatGPT, device code, API key, or access token", "https://github.com/openai/codex", "", ActionLogin, []string{"codex", "login"}},
		{"kiro", "Log in to Kiro", "kiro-cli", "Native browser flow; device flow remains a CLI option", "https://kiro.dev/docs/getting-started/installation/", "", ActionLogin, []string{"kiro-cli", "login"}},
		{"deepseek-harness", "Set up DeepSeek", "dsh", "Opens DeepSeek's Models page to store an API key and pick a model route; leave it running until the key is saved", "https://github.com/deepseek-ai/deepseek-harness", "", ActionSetup, []string{"dsh", "--profile", "web"}},
	}

	svc := New(foundExecutables(cases), nil)
	plans := svc.Plans(context.Background())
	if len(plans) != len(cases) {
		t.Fatalf("Plans() returned %d plans, want %d", len(plans), len(cases))
	}
	seen := make(map[string]bool, len(plans))
	for i, want := range cases {
		got := plans[i]
		wantLaunchMode := LaunchTerminal
		if seen[got.AgentID] {
			t.Fatalf("Plans() returned duplicate id %q", got.AgentID)
		}
		seen[got.AgentID] = true
		if got.AgentID != want.id || got.Action != want.action || got.LaunchMode != wantLaunchMode || !got.Available || got.Guidance != want.guidance || got.DocumentationURL != want.docs {
			t.Fatalf("plan %d = %#v, want id=%q action=%q available=true guidance=%q docs=%q", i, got, want.id, want.action, want.guidance, want.docs)
		}
		wantCommand := append([]string(nil), want.argv...)
		if len(wantCommand) > 0 {
			wantCommand[0] = "/test/bin/" + want.executable
		}
		if got.title != want.title || got.DisplayCommand != strings.Join(want.argv, " ") || !reflect.DeepEqual(got.command, wantCommand) || got.terminalInput != want.terminalInput {
			t.Fatalf("plan %q terminal = title %q display %q argv %#v, want title %q display %q argv %#v", want.id, got.title, got.DisplayCommand, got.command, want.title, strings.Join(want.argv, " "), wantCommand)
		}
		data, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `"command":`) || strings.Contains(string(data), `"terminalInput":`) || strings.Contains(string(data), `"initialInput":`) {
			t.Fatalf("plan %q serialized trusted command data: %s", want.id, data)
		}
	}
}
func TestUnknownPlanReturnsStableTargetError(t *testing.T) {
	t.Parallel()

	_, err := New(foundExecutables(nil), nil).Plan(context.Background(), "not-a-harness")
	var targetErr *apierr.Error
	if !errors.As(err, &targetErr) || targetErr.Kind != apierr.KindInvalid || targetErr.Code != "AGENT_AUTH_TARGET_UNKNOWN" {
		t.Fatalf("Plan() error = %v, want AGENT_AUTH_TARGET_UNKNOWN", err)
	}
}

func TestPlanMissingExecutableIsUnavailable(t *testing.T) {
	t.Parallel()

	plan, err := New(foundExecutables(nil), nil).Plan(context.Background(), "codex")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Available || plan.Reason == "" {
		t.Fatalf("Plan(codex) = %#v, want unavailable plan with useful reason", plan)
	}
}

func foundExecutables(cases []struct {
	id, title, executable, guidance, docs, terminalInput string
	action                                               Action
	argv                                                 []string
}) ExecutableFinder {
	found := map[string]string{}
	for _, tc := range cases {
		if tc.executable != "" {
			found[tc.executable] = "/test/bin/" + tc.executable
		}
	}
	return executableFinderFunc(func(name string) (string, error) {
		path, ok := found[name]
		if !ok {
			return "", errors.New("not found")
		}
		return path, nil
	})
}
