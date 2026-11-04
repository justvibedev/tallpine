package agentlaunch

import (
	"os"
	"reflect"
	"slices"
	"testing"
)

func TestInheritedHostIdentityKeysSelectsOnlyHostIdentity(t *testing.T) {
	environ := []string{
		// The environment a tallpine daemon inherits when the app is opened from a
		// Claude Code worker of the official AO.
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=sdk-ts",
		"CLAUDE_CODE_SESSION_ID=4533bedc-c4e1-4e04-876f-59d0d0719a2a",
		"CLAUDE_CODE_CHILD_SESSION=1",
		"CLAUDE_CODE_MESSAGING_SOCKET=/tmp/cc-socks/27801.sock",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret=with=equals",
		"CLAUDE_PID=27801",
		"AI_AGENT=claude-code_2-1-310_agent",
		"AO_SESSION_ID=tallpine-49",
		"AO_RUN_FILE=/Users/me/.tallpine/running.json",
		"AO_DATA_DIR=/Users/me/.tallpine/data",
		"AO_SOME_FUTURE_KEY=x",
		"TALLPINE_SESSION_ID=other-1",
		"TALLPINE_SHELL_TERMINAL=1",
		"CODEX_THREAD_ID=thread",
		"CLAUDECODE=1", // duplicate entries are reported once
		// User and daemon configuration that must survive.
		"PATH=/usr/bin:/bin",
		"HOME=/Users/me",
		"ANTHROPIC_API_KEY=sk-test",
		"CLAUDE_CONFIG_DIR=/Users/me/.claude-work",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"CLAUDE_CODE_EXECUTABLE=/Users/me/.local/bin/claude",
		"CODEX_HOME=/Users/me/.codex",
		"TALLPINE_DATA_DIR=/Users/me/.tallpine/data",
		"TALLPINE_RUN_FILE=/Users/me/.tallpine/running.json",
		"TALLPINE_PORT=3101",
		"TALLPINE_ACP_RUNTIME_DIR=/Applications/Tallpine.app/Contents/Resources/acp-runtime",
		"AOX=not-the-tallpine-namespace",
		"=C:=C:\\", // Windows drive-cwd pseudo variable
		"MALFORMED",
	}
	want := []string{
		"AI_AGENT",
		"AO_DATA_DIR",
		"AO_RUN_FILE",
		"AO_SESSION_ID",
		"AO_SOME_FUTURE_KEY",
		"CLAUDECODE",
		"CLAUDE_CODE_CHILD_SESSION",
		"CLAUDE_CODE_ENTRYPOINT",
		"CLAUDE_CODE_MESSAGING_SOCKET",
		"CLAUDE_CODE_MESSAGING_TOKEN",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_PID",
		"CODEX_THREAD_ID",
		"TALLPINE_SESSION_ID",
		"TALLPINE_SHELL_TERMINAL",
	}
	if got := InheritedHostIdentityKeys(environ); !slices.Equal(got, want) {
		t.Fatalf("InheritedHostIdentityKeys =\n  %v\nwant\n  %v", got, want)
	}
}

func TestScrubInheritedHostEnvUnsetsIdentityAndKeepsConfiguration(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "sdk-ts")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "parent-session")
	t.Setenv("AO_RUN_FILE", "/Users/me/.tallpine/running.json")
	t.Setenv("AO_SESSION_ID", "tallpine-49")
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/claude-home")
	t.Setenv("TALLPINE_DATA_DIR", "/tmp/vo-data")

	removed := ScrubInheritedHostEnv()

	for _, key := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SESSION_ID", "AO_RUN_FILE", "AO_SESSION_ID"} {
		if !slices.Contains(removed, key) {
			t.Errorf("removed keys %v do not include %s", removed, key)
		}
		if value, ok := os.LookupEnv(key); ok {
			t.Errorf("%s is still set to %q after scrub", key, value)
		}
	}
	if got := os.Getenv("CLAUDE_CONFIG_DIR"); got != "/tmp/claude-home" {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want user configuration kept", got)
	}
	if got := os.Getenv("TALLPINE_DATA_DIR"); got != "/tmp/vo-data" {
		t.Errorf("TALLPINE_DATA_DIR = %q, want daemon configuration kept", got)
	}
	if again := ScrubInheritedHostEnv(); slices.ContainsFunc(again, func(key string) bool {
		return key == "CLAUDECODE" || key == "AO_RUN_FILE"
	}) {
		t.Errorf("second scrub removed %v again", again)
	}
}

func TestScrubRemovesTallpineTwinsOfIdentityKeys(t *testing.T) {
	environ := []string{"TALLPINE_SESSION_ID=x", "tallpine_session_token=y", "TALLPINE_DATA_DIR=/keep", "PATH=/bin"}
	got := InheritedHostIdentityKeys(environ)
	want := []string{"TALLPINE_SESSION_ID", "tallpine_session_token"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v (TALLPINE_DATA_DIR is config, not identity)", got, want)
	}
}
