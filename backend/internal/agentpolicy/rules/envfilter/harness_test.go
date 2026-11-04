package envfilter_test

import (
	"slices"
	"testing"
)

var credentialEnv = append([]string{
	"OPENAI_API_KEY=o", "CODEX_HOME=/c", "ANTHROPIC_API_KEY=a", "CLAUDE_CONFIG_DIR=/cl", "CLAUDE_CODE_OAUTH_TOKEN=t", "CLAUDE_CODE_USE_BEDROCK=1",
	"AWS_ACCESS_KEY_ID=i", "AWS_REGION=r", "GOOGLE_APPLICATION_CREDENTIALS=/g", "GEMINI_API_KEY=g", "KIRO_TOKEN=k", "DEEPSEEK_API_KEY=d",
}, daemonEnv...)

func keptFor(t *testing.T, harness string) []string {
	t.Helper()
	return keys(outcome(t, "allowlist").ForHarness(harness).Filter(credentialEnv, false, nil).Keep)
}

func TestClaudeWorkerLacksOtherHarnessCredentials(t *testing.T) {
	got := keptFor(t, "claude-code")
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_HOME", "GEMINI_API_KEY", "KIRO_TOKEN", "DEEPSEEK_API_KEY"} {
		if slices.Contains(got, name) {
			t.Errorf("claude-code kept %s", name)
		}
	}
}

func TestClaudeBedrockAndVertexVarsSurvive(t *testing.T) {
	got := keptFor(t, "claude-code")
	for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_REGION", "GOOGLE_APPLICATION_CREDENTIALS"} {
		if !slices.Contains(got, name) {
			t.Errorf("claude-code lost %s", name)
		}
	}
}

func TestCodexWorkerLacksClaudeCredentials(t *testing.T) {
	got := keptFor(t, "codex")
	for _, name := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CONFIG_DIR", "AWS_ACCESS_KEY_ID", "GOOGLE_APPLICATION_CREDENTIALS"} {
		if slices.Contains(got, name) {
			t.Errorf("codex kept %s", name)
		}
	}
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_HOME"} {
		if !slices.Contains(got, name) {
			t.Errorf("codex lost %s", name)
		}
	}
}

func TestSharedVarsReachEveryHarness(t *testing.T) {
	for _, h := range []string{"claude-code", "codex", "kiro", "deepseek-harness", "fake", "", "unknown"} {
		got := keptFor(t, h)
		for _, name := range []string{"PATH", "HOME", "LANG", "LC_ALL", "HTTPS_PROXY", "no_proxy", "SSH_AUTH_SOCK"} {
			if !slices.Contains(got, name) {
				t.Errorf("%q lost shared %s", h, name)
			}
		}
	}
}

func TestUnknownHarnessKeepsEveryList(t *testing.T) {
	for _, h := range []string{"", "unknown"} {
		got := keptFor(t, h)
		for _, name := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "KIRO_TOKEN"} {
			if !slices.Contains(got, name) {
				t.Errorf("%q lost %s", h, name)
			}
		}
	}
}
