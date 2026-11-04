package sessionmanager

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

func scratchTestManager(t *testing.T, gradle bool) *Manager {
	t.Helper()
	return &Manager{
		dataDir:           t.TempDir(),
		sessionGradleHome: gradle,
		executable:        func() (string, error) { return filepath.Join("/opt", "aod", "tallpine"), nil },
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestRuntimeEnvGivesEachSessionItsOwnTempDir(t *testing.T) {
	t.Setenv(envGradleUserHome, "")
	m := scratchTestManager(t, false)
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	want := sessionscratch.TempDir(m.dataDir, "mer-1")
	for _, key := range scratchTempKeys {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q", key, env[key], want)
		}
	}
	if got := env[sessionscratch.TempPrefixEnv]; got != sessionscratch.TempPrefix(want) {
		t.Fatalf("TMPPREFIX = %q, want zsh temp files in %q (B521)", got, want)
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("session temp dir not created at launch: %v", err)
	}
	if _, ok := env[envGradleUserHome]; ok {
		t.Fatalf("GRADLE_USER_HOME set while the gradle home is disabled: %v", env)
	}
	if other := m.runtimeEnv("mer-2", "mer", "", nil); other["TMPDIR"] == env["TMPDIR"] {
		t.Fatalf("two sessions share a temp dir: %q", other["TMPDIR"])
	}
}

func TestRuntimeEnvKeepsProjectTempDir(t *testing.T) {
	t.Parallel()

	m := scratchTestManager(t, false)
	env := m.runtimeEnv("mer-1", "mer", "", map[string]string{"TMPDIR": "/project/tmp"})
	if env["TMPDIR"] != "/project/tmp" {
		t.Fatalf("TMPDIR = %q, want the project's", env["TMPDIR"])
	}
	if _, ok := env["TMP"]; ok {
		t.Fatalf("TMP set although the project chose its temp dir: %v", env)
	}
	if _, err := os.Stat(sessionscratch.TempDir(m.dataDir, "mer-1")); !os.IsNotExist(err) {
		t.Fatalf("session temp dir created although unused: %v", err)
	}
}

func TestRuntimeEnvPointsGradleAtTheProjectHome(t *testing.T) {
	t.Setenv(envGradleUserHome, "")
	t.Setenv("HOME", t.TempDir())
	m := scratchTestManager(t, true)
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	want := sessionscratch.GradleHome(m.dataDir, "mer")
	if env[envGradleUserHome] != want {
		t.Fatalf("GRADLE_USER_HOME = %q, want %q", env[envGradleUserHome], want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("project gradle home not created: %v", err)
	}
	if other := m.runtimeEnv("mer-2", "mer", "", nil); other[envGradleUserHome] != want {
		t.Fatalf("sessions of one project do not share its gradle home: %q", other[envGradleUserHome])
	}
}

func TestRuntimeEnvKeepsAnExplicitGradleHome(t *testing.T) {
	m := scratchTestManager(t, true)
	t.Setenv(envGradleUserHome, "")
	env := m.runtimeEnv("mer-1", "mer", "", map[string]string{envGradleUserHome: "/project/gradle"})
	if env[envGradleUserHome] != "/project/gradle" {
		t.Fatalf("project GRADLE_USER_HOME replaced: %q", env[envGradleUserHome])
	}
	t.Setenv(envGradleUserHome, "/daemon/gradle")
	env = m.runtimeEnv("mer-1", "mer", "", nil)
	if _, ok := env[envGradleUserHome]; ok {
		t.Fatalf("daemon-inherited GRADLE_USER_HOME overridden: %q", env[envGradleUserHome])
	}
}

func TestTeardownEnvDoesNotRecreateTheTempDir(t *testing.T) {
	t.Parallel()

	m := scratchTestManager(t, true)
	env := m.teardownRuntimeEnv("mer-1", "mer", "", nil)
	if _, ok := env["TMPDIR"]; ok {
		t.Fatalf("teardown env carries the session temp dir: %v", env)
	}
	if _, err := os.Stat(filepath.Join(m.dataDir, sessionscratch.TempRoot)); !os.IsNotExist(err) {
		t.Fatalf("teardown created scratch dirs: %v", err)
	}
	if env[EnvSessionID] != "mer-1" {
		t.Fatalf("teardown env lost the session id: %v", env)
	}
}

// Claude Code ignores TMPDIR and uses /tmp/claude-<uid> (shared by every
// session) unless CLAUDE_CODE_TMPDIR is set (B429).
func TestRuntimeEnvPointsClaudeTempAtSessionDir(t *testing.T) {
	t.Setenv(envGradleUserHome, "")
	m := scratchTestManager(t, false)
	env := m.runtimeEnv("mer-1", "mer", "", nil)
	want := sessionscratch.TempDir(m.dataDir, "mer-1")
	for _, key := range []string{"CLAUDE_CODE_TMPDIR", "CLAUDE_TMPDIR"} {
		if env[key] != want {
			t.Fatalf("%s = %q, want %q", key, env[key], want)
		}
	}
}
