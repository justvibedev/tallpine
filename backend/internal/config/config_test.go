package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear every recognised var so we observe pure defaults regardless of the
	// surrounding environment.
	for _, k := range []string{"TALLPINE_PORT", "TALLPINE_REQUEST_TIMEOUT", "TALLPINE_SPAWN_TIMEOUT", "TALLPINE_SHUTDOWN_TIMEOUT", "TALLPINE_RUN_FILE", "TALLPINE_DATA_DIR", "TALLPINE_AGENT", "TALLPINE_ALLOWED_ORIGINS", "TALLPINE_HARNESS_UPDATE_CHECKS", "TALLPINE_HARNESS_UPDATE_INTERVAL"} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != LoopbackHost {
		t.Errorf("Host = %q, want %q", cfg.Host, LoopbackHost)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("RequestTimeout = %s, want %s", cfg.RequestTimeout, DefaultRequestTimeout)
	}
	if cfg.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", cfg.ShutdownTimeout, DefaultShutdownTimeout)
	}
	if cfg.RunFilePath == "" {
		t.Error("RunFilePath is empty, want a resolved default path")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	wantRunFilePath := filepath.Join(homeDir, ".tallpine", "running.json")
	if cfg.RunFilePath != wantRunFilePath {
		t.Errorf("RunFilePath = %q, want %q", cfg.RunFilePath, wantRunFilePath)
	}
	if cfg.DataDir == "" {
		t.Error("DataDir is empty, want a resolved default path")
	}
	wantDataDir := filepath.Join(homeDir, ".tallpine", "data")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	if wantStateDir := filepath.Join(homeDir, ".tallpine"); cfg.StateDir != wantStateDir {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, wantStateDir)
	}
}

func TestLoadAbsolutizesRelativeOverrides(t *testing.T) {
	// A relative override must be resolved to absolute at Load time. The daemon
	// chdir's into its data dir at startup, so a relative path left as-is would
	// be re-resolved against the new cwd and double-nest state.
	t.Setenv("TALLPINE_RUN_FILE", "rel-running.json")
	t.Setenv("TALLPINE_DATA_DIR", "rel-data")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(cfg.RunFilePath) {
		t.Errorf("RunFilePath = %q, want absolute", cfg.RunFilePath)
	}
	if !filepath.IsAbs(cfg.DataDir) {
		t.Errorf("DataDir = %q, want absolute", cfg.DataDir)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "rel-data"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if cfg.StateDir != cfg.DataDir {
		t.Errorf("StateDir = %q, want explicit DataDir %q", cfg.StateDir, cfg.DataDir)
	}
	if want := filepath.Join(cwd, "rel-running.json"); cfg.RunFilePath != want {
		t.Errorf("RunFilePath = %q, want %q", cfg.RunFilePath, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	overrideDir := t.TempDir()
	runFilePath := filepath.Join(overrideDir, "tallpine-test-running.json")
	dataDir := filepath.Join(overrideDir, "tallpine-test-data")

	t.Setenv("TALLPINE_PORT", "4002")
	t.Setenv("TALLPINE_REQUEST_TIMEOUT", "5s")
	t.Setenv("TALLPINE_SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("TALLPINE_SPAWN_TIMEOUT", "4m")
	t.Setenv("TALLPINE_RUN_FILE", runFilePath)
	t.Setenv("TALLPINE_DATA_DIR", dataDir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr() != "127.0.0.1:4002" {
		t.Errorf("Addr() = %q, want 127.0.0.1:4002", cfg.Addr())
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Errorf("RequestTimeout = %s, want 5s", cfg.RequestTimeout)
	}
	if cfg.SpawnTimeout != 4*time.Minute {
		t.Errorf("SpawnTimeout = %s, want 4m", cfg.SpawnTimeout)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 3s", cfg.ShutdownTimeout)
	}
	if cfg.RunFilePath != runFilePath {
		t.Errorf("RunFilePath = %q, want %q", cfg.RunFilePath, runFilePath)
	}
	if cfg.DataDir != dataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dataDir)
	}
	if cfg.StateDir != dataDir {
		t.Errorf("StateDir = %q, want explicit DataDir %q", cfg.StateDir, dataDir)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"non-numeric port", map[string]string{"TALLPINE_PORT": "abc"}},
		{"port out of range", map[string]string{"TALLPINE_PORT": "70000"}},
		{"bad request timeout", map[string]string{"TALLPINE_REQUEST_TIMEOUT": "soon"}},
		{"bad spawn timeout", map[string]string{"TALLPINE_SPAWN_TIMEOUT": "0s"}},
		{"bad tmux command timeout", map[string]string{"TALLPINE_TMUX_COMMAND_TIMEOUT": "soon"}},
		{"zero tmux command timeout", map[string]string{"TALLPINE_TMUX_COMMAND_TIMEOUT": "0s"}},
		{"bad shutdown timeout", map[string]string{"TALLPINE_SHUTDOWN_TIMEOUT": "later"}},
		{"zero request timeout", map[string]string{"TALLPINE_REQUEST_TIMEOUT": "0s"}},
		{"negative request timeout", map[string]string{"TALLPINE_REQUEST_TIMEOUT": "-1s"}},
		{"zero shutdown timeout", map[string]string{"TALLPINE_SHUTDOWN_TIMEOUT": "0s"}},
		{"negative shutdown timeout", map[string]string{"TALLPINE_SHUTDOWN_TIMEOUT": "-5s"}},
		{"null origin", map[string]string{"TALLPINE_ALLOWED_ORIGINS": "app://renderer,null"}},
		{"wildcard origin", map[string]string{"TALLPINE_ALLOWED_ORIGINS": "*"}},
		{"non-numeric artifact versions keep", map[string]string{"TALLPINE_ARTIFACT_VERSIONS_KEEP": "many"}},
		{"bad artifact size warn", map[string]string{"TALLPINE_ARTIFACT_SIZE_WARN_MB": "big"}},
		{"zero artifact size warn", map[string]string{"TALLPINE_ARTIFACT_SIZE_WARN_MB": "0"}},
		{"zero artifact versions keep", map[string]string{"TALLPINE_ARTIFACT_VERSIONS_KEEP": "0"}},
		{"huge artifact versions keep", map[string]string{"TALLPINE_ARTIFACT_VERSIONS_KEEP": "100000"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load() = nil error, want error")
			}
		})
	}
}

func TestLoadArtifactVersionsKeep(t *testing.T) {
	t.Setenv("TALLPINE_ARTIFACT_VERSIONS_KEEP", "")
	cfg, err := Load()
	if err != nil || cfg.ArtifactVersionsKeep != DefaultArtifactVersionsKeep {
		t.Fatalf("default = %d, err %v", cfg.ArtifactVersionsKeep, err)
	}
	t.Setenv("TALLPINE_ARTIFACT_VERSIONS_KEEP", " 7 ")
	cfg, err = Load()
	if err != nil || cfg.ArtifactVersionsKeep != 7 {
		t.Fatalf("configured = %d, err %v", cfg.ArtifactVersionsKeep, err)
	}
}

func TestLoadAllowedOrigins(t *testing.T) {
	t.Run("default includes the packaged renderer origin", func(t *testing.T) {
		t.Setenv("TALLPINE_ALLOWED_ORIGINS", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		found := false
		for _, origin := range cfg.AllowedOrigins {
			if origin == "app://renderer" {
				found = true
			}
		}
		if !found {
			t.Errorf("AllowedOrigins = %v, want app://renderer included", cfg.AllowedOrigins)
		}
	})

	t.Run("override replaces defaults and trims entries", func(t *testing.T) {
		t.Setenv("TALLPINE_ALLOWED_ORIGINS", " app://renderer , http://localhost:9999 ,")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := []string{"app://renderer", "http://localhost:9999"}
		if len(cfg.AllowedOrigins) != len(want) {
			t.Fatalf("AllowedOrigins = %v, want %v", cfg.AllowedOrigins, want)
		}
		for i, origin := range want {
			if cfg.AllowedOrigins[i] != origin {
				t.Errorf("AllowedOrigins[%d] = %q, want %q", i, cfg.AllowedOrigins[i], origin)
			}
		}
	})
}

func TestLoadOfferingDefaults(t *testing.T) {
	// Clear the offering vars so we observe pure defaults: no client identity,
	// cloud off, local on.
	for _, k := range []string{"TALLPINE_CLIENT", "TALLPINE_LOCAL_OFFERING"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Client != "" {
		t.Errorf("Client = %q, want empty", cfg.Client)
	}
	if !cfg.LocalOffering {
		t.Error("LocalOffering = false, want true by default")
	}
}

func TestLoadOfferingOverrides(t *testing.T) {
	t.Setenv("TALLPINE_CLIENT", "  eleven_x  ")
	t.Setenv("TALLPINE_LOCAL_OFFERING", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Client != "eleven_x" {
		t.Errorf("Client = %q, want trimmed eleven_x", cfg.Client)
	}
	if cfg.LocalOffering {
		t.Error("LocalOffering = true, want explicit false to stick")
	}
}

// The offering toggles must accept the numeric spellings the supervisor may
// hand down: 0 is the only way local goes off.
func TestLoadOfferingNumericToggles(t *testing.T) {
	t.Setenv("TALLPINE_LOCAL_OFFERING", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LocalOffering {
		t.Error("LocalOffering = true, want false for TALLPINE_LOCAL_OFFERING=0")
	}
}

func TestLoadOfferingInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"bad local offering toggle", map[string]string{"TALLPINE_LOCAL_OFFERING": "maybe"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load() = nil error, want error")
			}
		})
	}
}

func TestLoadGitLabDefaults(t *testing.T) {
	// Clear the GitLab config vars so we observe pure defaults.
	for _, k := range []string{"TALLPINE_GITLAB_ALLOWED_HOSTS", "TALLPINE_GITLAB_HOST_TOKENS"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitLab.AllowedHosts != nil {
		t.Errorf("GitLab.AllowedHosts = %v, want nil", cfg.GitLab.AllowedHosts)
	}
	if cfg.GitLab.HostTokens != nil {
		t.Errorf("GitLab.HostTokens = %v, want nil", cfg.GitLab.HostTokens)
	}
}

func TestLoadGitLabAllowedHosts(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want []string
	}{
		{"single host", "gitlab.mycompany.com", []string{"gitlab.mycompany.com"}},
		{"comma-separated", "gitlab.mycompany.com,gitlab.internal.corp", []string{"gitlab.mycompany.com", "gitlab.internal.corp"}},
		{"trimmed whitespace", " gitlab.mycompany.com , gitlab.internal.corp ", []string{"gitlab.mycompany.com", "gitlab.internal.corp"}},
		{"empty entries skipped", "gitlab.mycompany.com,,gitlab.internal.corp,", []string{"gitlab.mycompany.com", "gitlab.internal.corp"}},
		{"with port", "gitlab.internal:8443", []string{"gitlab.internal:8443"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TALLPINE_GITLAB_ALLOWED_HOSTS", tc.env)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.GitLab.AllowedHosts) != len(tc.want) {
				t.Fatalf("AllowedHosts = %v, want %v", cfg.GitLab.AllowedHosts, tc.want)
			}
			for i, h := range tc.want {
				if cfg.GitLab.AllowedHosts[i] != h {
					t.Errorf("AllowedHosts[%d] = %q, want %q", i, cfg.GitLab.AllowedHosts[i], h)
				}
			}
		})
	}
}

func TestLoadGitLabHostTokens(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want map[string]string
	}{
		{
			name: "single host=token",
			env:  "gitlab.mycompany.com=token-1",
			want: map[string]string{"gitlab.mycompany.com": "token-1"},
		},
		{
			name: "multiple host=token pairs",
			env:  "gitlab.mycompany.com=token-1,gitlab.internal.corp=token-2",
			want: map[string]string{
				"gitlab.mycompany.com": "token-1",
				"gitlab.internal.corp": "token-2",
			},
		},
		{
			name: "trimmed whitespace around entries",
			env:  " gitlab.mycompany.com = token-1 , gitlab.internal.corp = token-2 ",
			want: map[string]string{
				"gitlab.mycompany.com": "token-1",
				"gitlab.internal.corp": "token-2",
			},
		},
		{
			name: "host with port",
			env:  "gitlab.internal:8443=token-port",
			want: map[string]string{"gitlab.internal:8443": "token-port"},
		},
		{
			name: "empty entries skipped",
			env:  "gitlab.mycompany.com=token-1,,",
			want: map[string]string{"gitlab.mycompany.com": "token-1"},
		},
		{
			name: "entry without equals skipped",
			env:  "gitlab.mycompany.com=token-1,not-a-pair",
			want: map[string]string{"gitlab.mycompany.com": "token-1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TALLPINE_GITLAB_HOST_TOKENS", tc.env)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.GitLab.HostTokens) != len(tc.want) {
				t.Fatalf("HostTokens = %v, want %v", cfg.GitLab.HostTokens, tc.want)
			}
			for k, v := range tc.want {
				got, ok := cfg.GitLab.HostTokens[k]
				if !ok {
					t.Errorf("HostTokens missing key %q", k)
					continue
				}
				if got != v {
					t.Errorf("HostTokens[%q] = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestLoadGitLabInvalidHostTokens(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{"token contains equals", "gitlab.mycompany.com=token=with=equals"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TALLPINE_GITLAB_HOST_TOKENS", tc.env)
			_, err := Load()
			if err == nil {
				t.Fatal("Load() = nil error, want error for malformed TALLPINE_GITLAB_HOST_TOKENS")
			}
		})
	}
}

func TestLoadTrackerIntakeDefaultsOff(t *testing.T) {
	// A blank-but-present value counts as unset. Load() feeds every tallpine
	// command, so treating it as malformed would fail `tallpine status`/`tallpine stop`
	// over an empty export rather than just leaving intake off.
	for _, raw := range []string{"", " ", "\t"} {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			t.Setenv("TALLPINE_TRACKER_INTAKE", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TrackerIntake {
				t.Error("TrackerIntake = true, want false by default")
			}
		})
	}
}

func TestLoadTrackerIntakeToggle(t *testing.T) {
	on := []string{"on", "true", "1", "yes"}
	off := []string{"off", "false", "0", "no"}
	for _, raw := range on {
		t.Run("on/"+raw, func(t *testing.T) {
			t.Setenv("TALLPINE_TRACKER_INTAKE", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !cfg.TrackerIntake {
				t.Errorf("TrackerIntake = false for %q, want true", raw)
			}
		})
	}
	for _, raw := range off {
		t.Run("off/"+raw, func(t *testing.T) {
			t.Setenv("TALLPINE_TRACKER_INTAKE", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TrackerIntake {
				t.Errorf("TrackerIntake = true for %q, want false", raw)
			}
		})
	}
}

func TestLoadTrackerIntakeRejectsGarbage(t *testing.T) {
	t.Setenv("TALLPINE_TRACKER_INTAKE", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("Load() = nil error, want error for malformed TALLPINE_TRACKER_INTAKE")
	}
}

// The daemon proxies desktop requests to the agent browser, so it may only
// ever point at this host.
func TestLoadAgentBrowserURL(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
		{" http://localhost:8080/ ", "http://localhost:8080"},
		{"http://10.0.0.5:8080", ""},
		{"http://example.com:8080", ""},
		{"https://127.0.0.1:8080", ""},
		{"http://user:pw@127.0.0.1:8080", ""},
		{"http://127.0.0.1:8080/?token=x", ""},
	} {
		t.Setenv("TALLPINE_AGENT_BROWSER_URL", tc.raw)
		t.Setenv("TALLPINE_AGENT_BROWSER_TOKEN", "rb-secret")
		cfg, err := Load()
		if tc.want == "" {
			if err == nil {
				t.Errorf("%q: loaded, want an error", tc.raw)
			}
			continue
		}
		if err != nil || cfg.AgentBrowserURL != tc.want || cfg.AgentBrowserToken != "rb-secret" {
			t.Errorf("%q: got %q, %q, %v; want %q", tc.raw, cfg.AgentBrowserURL, cfg.AgentBrowserToken, err, tc.want)
		}
	}
}

// The agent browser refuses every request without its exact token: a padded
// one would never match, and a missing one must stop the daemon with a reason
// rather than leave the agent tab silently empty.
func TestLoadAgentBrowserToken(t *testing.T) {
	t.Setenv("TALLPINE_AGENT_BROWSER_URL", "http://127.0.0.1:8080")
	t.Setenv("TALLPINE_AGENT_BROWSER_TOKEN", " rb-secret\n")
	if cfg, err := Load(); err != nil || cfg.AgentBrowserToken != "rb-secret" {
		t.Errorf("padded token: got %q, %v; want rb-secret", cfg.AgentBrowserToken, err)
	}
	for _, token := range []string{"", " \t"} {
		t.Setenv("TALLPINE_AGENT_BROWSER_TOKEN", token)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TALLPINE_AGENT_BROWSER_TOKEN") {
			t.Errorf("token %q: got %v, want an error naming TALLPINE_AGENT_BROWSER_TOKEN", token, err)
		}
	}
}

func TestLoadHarnessUpdateChecksToggle(t *testing.T) {
	t.Setenv("TALLPINE_HARNESS_UPDATE_CHECKS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HarnessUpdateChecks {
		t.Fatal("harness update checks are off by default")
	}
	t.Setenv("TALLPINE_HARNESS_UPDATE_CHECKS", "off")
	if cfg, err = Load(); err != nil || cfg.HarnessUpdateChecks {
		t.Fatalf("TALLPINE_HARNESS_UPDATE_CHECKS=off: checks=%t err=%v", cfg.HarnessUpdateChecks, err)
	}
	t.Setenv("TALLPINE_HARNESS_UPDATE_CHECKS", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("invalid TALLPINE_HARNESS_UPDATE_CHECKS value was accepted")
	}
}

func TestLoadUpdateWhileRunningToggle(t *testing.T) {
	t.Setenv("TALLPINE_UPDATE_WHILE_RUNNING", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpdateWhileRunning {
		t.Fatal("update while running is off by default")
	}
	t.Setenv("TALLPINE_UPDATE_WHILE_RUNNING", "off")
	if cfg, err = Load(); err != nil || cfg.UpdateWhileRunning {
		t.Fatalf("TALLPINE_UPDATE_WHILE_RUNNING=off: enabled=%t err=%v", cfg.UpdateWhileRunning, err)
	}
	t.Setenv("TALLPINE_UPDATE_WHILE_RUNNING", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("invalid TALLPINE_UPDATE_WHILE_RUNNING value was accepted")
	}
}

func TestLoadVersionRefreshSettings(t *testing.T) {
	t.Setenv("TALLPINE_VERSION_REFRESH", "")
	t.Setenv("TALLPINE_VERSION_REFRESH_IDLE", "")
	cfg, err := Load()
	if err != nil || !cfg.VersionRefresh || cfg.VersionRefreshIdle != DefaultVersionRefreshIdle {
		t.Fatalf("defaults: refresh=%t idle=%s err=%v", cfg.VersionRefresh, cfg.VersionRefreshIdle, err)
	}
	t.Setenv("TALLPINE_VERSION_REFRESH", "off")
	t.Setenv("TALLPINE_VERSION_REFRESH_IDLE", "30m")
	if cfg, err = Load(); err != nil || cfg.VersionRefresh || cfg.VersionRefreshIdle != 30*time.Minute {
		t.Fatalf("overrides: refresh=%t idle=%s err=%v", cfg.VersionRefresh, cfg.VersionRefreshIdle, err)
	}
	t.Setenv("TALLPINE_VERSION_REFRESH_IDLE", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("invalid TALLPINE_VERSION_REFRESH_IDLE was accepted")
	}
}

func TestLoadSessionGradleHome(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_GRADLE_HOME", "")
	cfg, err := Load()
	if err != nil || !cfg.SessionGradleHome {
		t.Fatalf("default: gradle home=%t err=%v", cfg.SessionGradleHome, err)
	}
	t.Setenv("TALLPINE_SESSION_GRADLE_HOME", "off")
	if cfg, err = Load(); err != nil || cfg.SessionGradleHome {
		t.Fatalf("off: gradle home=%t err=%v", cfg.SessionGradleHome, err)
	}
	t.Setenv("TALLPINE_SESSION_GRADLE_HOME", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("invalid TALLPINE_SESSION_GRADLE_HOME was accepted")
	}
}

func TestLoadWorkerIdleNudge(t *testing.T) {
	t.Setenv("TALLPINE_WORKER_IDLE_NUDGE", "")
	cfg, err := Load()
	if err != nil || cfg.WorkerIdleNudge != DefaultWorkerIdleNudge {
		t.Fatalf("default: nudge=%s err=%v", cfg.WorkerIdleNudge, err)
	}
	for raw, want := range map[string]time.Duration{"20m": 20 * time.Minute, "off": 0, "OFF": 0, "0": 0} {
		t.Setenv("TALLPINE_WORKER_IDLE_NUDGE", raw)
		if cfg, err = Load(); err != nil || cfg.WorkerIdleNudge != want {
			t.Fatalf("%q: nudge=%s err=%v, want %s", raw, cfg.WorkerIdleNudge, err, want)
		}
	}
	for _, raw := range []string{"30s", "soon", "-5m"} {
		t.Setenv("TALLPINE_WORKER_IDLE_NUDGE", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("invalid TALLPINE_WORKER_IDLE_NUDGE %q was accepted", raw)
		}
	}
}

func TestLoadReviewPassDeadline(t *testing.T) {
	t.Setenv("TALLPINE_REVIEW_PASS_DEADLINE", "")
	cfg, err := Load()
	if err != nil || cfg.ReviewPassDeadline != DefaultReviewPassDeadline {
		t.Fatalf("default: deadline=%s err=%v", cfg.ReviewPassDeadline, err)
	}
	for raw, want := range map[string]time.Duration{"90m": 90 * time.Minute, "10m": 10 * time.Minute, "off": 0, "0": 0} {
		t.Setenv("TALLPINE_REVIEW_PASS_DEADLINE", raw)
		if cfg, err = Load(); err != nil || cfg.ReviewPassDeadline != want {
			t.Fatalf("%q: deadline=%s err=%v, want %s", raw, cfg.ReviewPassDeadline, err, want)
		}
	}
	for _, raw := range []string{"5m", "later", "-1h"} {
		t.Setenv("TALLPINE_REVIEW_PASS_DEADLINE", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("invalid TALLPINE_REVIEW_PASS_DEADLINE %q was accepted", raw)
		}
	}
}

func TestLoadReviewIdleGrace(t *testing.T) {
	t.Setenv("TALLPINE_REVIEW_IDLE_GRACE", "")
	cfg, err := Load()
	if err != nil || cfg.ReviewIdleGrace != DefaultReviewIdleGrace {
		t.Fatalf("default: grace=%s err=%v", cfg.ReviewIdleGrace, err)
	}
	for raw, want := range map[string]time.Duration{"30m": 30 * time.Minute, "1m": time.Minute, "off": 0} {
		t.Setenv("TALLPINE_REVIEW_IDLE_GRACE", raw)
		if cfg, err = Load(); err != nil || cfg.ReviewIdleGrace != want {
			t.Fatalf("%q: grace=%s err=%v, want %s", raw, cfg.ReviewIdleGrace, err, want)
		}
	}
	for _, raw := range []string{"30s", "never", "-1m"} {
		t.Setenv("TALLPINE_REVIEW_IDLE_GRACE", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("invalid TALLPINE_REVIEW_IDLE_GRACE %q was accepted", raw)
		}
	}
}

func TestLoadPhaseThreeToggles(t *testing.T) {
	t.Setenv("TALLPINE_TUI_VERSION_REFRESH", "")
	t.Setenv("TALLPINE_WINDOWS_UPDATE_WHEN_IDLE", "")
	cfg, err := Load()
	if err != nil || !cfg.TUIVersionRefresh || !cfg.WindowsUpdateWhenIdle {
		t.Fatalf("defaults: tui=%t windows=%t err=%v", cfg.TUIVersionRefresh, cfg.WindowsUpdateWhenIdle, err)
	}
	t.Setenv("TALLPINE_TUI_VERSION_REFRESH", "off")
	t.Setenv("TALLPINE_WINDOWS_UPDATE_WHEN_IDLE", "off")
	if cfg, err = Load(); err != nil || cfg.TUIVersionRefresh || cfg.WindowsUpdateWhenIdle {
		t.Fatalf("off: tui=%t windows=%t err=%v", cfg.TUIVersionRefresh, cfg.WindowsUpdateWhenIdle, err)
	}
}

func TestLoadManagedHarnessInstallsToggle(t *testing.T) {
	t.Setenv("TALLPINE_MANAGED_HARNESS_INSTALLS", "")
	cfg, err := Load()
	if err != nil || !cfg.ManagedHarnessInstalls {
		t.Fatalf("default managed installs = %t, %v", cfg.ManagedHarnessInstalls, err)
	}
	t.Setenv("TALLPINE_MANAGED_HARNESS_INSTALLS", "off")
	if cfg, err = Load(); err != nil || cfg.ManagedHarnessInstalls {
		t.Fatalf("off: managed installs = %t, %v", cfg.ManagedHarnessInstalls, err)
	}
}

func TestLoadHarnessUpdateInterval(t *testing.T) {
	t.Setenv("TALLPINE_HARNESS_UPDATE_INTERVAL", "")
	cfg, err := Load()
	if err != nil || cfg.HarnessUpdateInterval != DefaultHarnessUpdateInterval {
		t.Fatalf("default interval = %s, err=%v; want %s", cfg.HarnessUpdateInterval, err, DefaultHarnessUpdateInterval)
	}
	t.Setenv("TALLPINE_HARNESS_UPDATE_INTERVAL", "12h")
	if cfg, err = Load(); err != nil || cfg.HarnessUpdateInterval != 12*time.Hour {
		t.Fatalf("12h interval = %s, err=%v", cfg.HarnessUpdateInterval, err)
	}
	for _, raw := range []string{"30m", "0s", "-1h", "soon"} {
		t.Setenv("TALLPINE_HARNESS_UPDATE_INTERVAL", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("TALLPINE_HARNESS_UPDATE_INTERVAL=%q was accepted", raw)
		}
	}
}

func TestLoadAuthOffNeedsSecondFlag(t *testing.T) {
	for _, tc := range []struct {
		name, unsafeDev     string
		wantOff, wantIgnore bool
	}{
		{"auth off alone is ignored", "", false, true},
		{"auth off with unsafe-dev flag is honored", "1", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TALLPINE_RUN_FILE", filepath.Join(dir, "running.json"))
			t.Setenv("TALLPINE_DATA_DIR", filepath.Join(dir, "data"))
			t.Setenv("TALLPINE_DAEMON_AUTH", "off")
			t.Setenv("TALLPINE_DAEMON_AUTH_UNSAFE_DEV", tc.unsafeDev)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DaemonAuthDisabled != tc.wantOff || cfg.DaemonAuthOffIgnored != tc.wantIgnore {
				t.Errorf("disabled=%v ignored=%v, want %v %v", cfg.DaemonAuthDisabled, cfg.DaemonAuthOffIgnored, tc.wantOff, tc.wantIgnore)
			}
		})
	}
}

func TestLoadReadsTallpineEnvBeforeVO(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TALLPINE_DATA_DIR", filepath.Join(dir, "vo-data"))
	t.Setenv("TALLPINE_RUN_FILE", filepath.Join(dir, "vo-running.json"))
	t.Setenv("TALLPINE_PORT", "4101")
	t.Setenv("TALLPINE_REQUEST_TIMEOUT", "7s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load (old names): %v", err)
	}
	if cfg.Port != 4101 || cfg.RequestTimeout != 7*time.Second || cfg.DataDir != filepath.Join(dir, "vo-data") {
		t.Fatalf("old names ignored: port %d timeout %s data %q", cfg.Port, cfg.RequestTimeout, cfg.DataDir)
	}

	t.Setenv("TALLPINE_DATA_DIR", filepath.Join(dir, "tp-data"))
	t.Setenv("TALLPINE_RUN_FILE", filepath.Join(dir, "tp-running.json"))
	t.Setenv("TALLPINE_PORT", "4102")
	t.Setenv("TALLPINE_REQUEST_TIMEOUT", "9s")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load (both names): %v", err)
	}
	if cfg.Port != 4102 || cfg.RequestTimeout != 9*time.Second || cfg.DataDir != filepath.Join(dir, "tp-data") || cfg.RunFilePath != filepath.Join(dir, "tp-running.json") {
		t.Fatalf("TALLPINE_ must win: port %d timeout %s data %q run %q", cfg.Port, cfg.RequestTimeout, cfg.DataDir, cfg.RunFilePath)
	}
}
