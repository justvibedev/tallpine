// Package config loads the daemon's runtime configuration. The HTTP daemon is
// a loopback-only sidecar: it binds 127.0.0.1, takes no public traffic, and
// reads everything it needs from the environment with sane defaults so it can
// boot with zero configuration in development.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
)

const (
	// LoopbackHost is the only host the daemon ever binds. There is deliberately
	// no TALLPINE_HOST env var: the daemon has no auth/CORS/TLS and a stray
	// TALLPINE_HOST=0.0.0.0 would turn it into a public no-auth service. If a
	// non-default loopback (e.g. ::1, 127.0.0.2) is ever needed, add it back with
	// an IsLoopback() validator — not a raw env read.
	LoopbackHost = "127.0.0.1"
	// DefaultPort is the single port for REST, terminal mux, health, and control.
	DefaultPort = brand.DefaultPort
	// DefaultRequestTimeout bounds a single REST request. Long-lived terminal mux
	// connections are mounted outside this timeout.
	DefaultRequestTimeout = 60 * time.Second
	// DefaultShutdownTimeout is the hard cap on graceful shutdown. After this
	// the process exits even if connections are still draining.
	DefaultShutdownTimeout = 10 * time.Second
	// DefaultHarnessUpdateInterval is how often the daemon re-checks installed
	// harnesses for updates after its startup check. A full pass probes each
	// harness CLI, so it runs a few times a day rather than continuously.
	DefaultHarnessUpdateInterval = 6 * time.Hour
	// DefaultVersionRefreshIdle is how long an open session must be quiet before
	// it moves to a newly installed harness release. Provider prompt caches
	// expire well within it, so the move costs no cached context.
	DefaultVersionRefreshIdle = 10 * time.Minute
	// DefaultWorkerIdleNudge is how long a silent idle worker waits before Tallpine
	// nudges it.
	DefaultWorkerIdleNudge = 10 * time.Minute
	// MinWorkerIdleNudge is the shortest TALLPINE_WORKER_IDLE_NUDGE accepted.
	MinWorkerIdleNudge = time.Minute
	// DefaultReviewPassDeadline is how long a Tallpine review pass may run without
	// submitting a result before Tallpine fails it. A healthy review of a large PR
	// finishes well within it.
	DefaultReviewPassDeadline = 45 * time.Minute
	// MinReviewPassDeadline is the shortest TALLPINE_REVIEW_PASS_DEADLINE accepted.
	MinReviewPassDeadline = 10 * time.Minute
	// DefaultReviewIdleGrace is how long a terminal reviewer may sit idle at
	// its prompt with a pass running before Tallpine fails that pass.
	DefaultReviewIdleGrace = 10 * time.Minute
	// MinReviewIdleGrace is the shortest TALLPINE_REVIEW_IDLE_GRACE accepted.
	MinReviewIdleGrace = time.Minute
	// MinHarnessUpdateInterval is the shortest TALLPINE_HARNESS_UPDATE_INTERVAL accepted.
	MinHarnessUpdateInterval = time.Hour
	// DefaultArtifactVersionsKeep is the default per-artifact version history
	// depth.
	DefaultArtifactVersionsKeep = 20
	// MaxArtifactVersionsKeep is the largest TALLPINE_ARTIFACT_VERSIONS_KEEP.
	MaxArtifactVersionsKeep = 1000
	// DefaultArtifactSizeWarnMB is the default TALLPINE_ARTIFACT_SIZE_WARN_MB.
	DefaultArtifactSizeWarnMB = 1024
	// DefaultAgent is the compatibility value used when TALLPINE_AGENT is unset. The
	// daemon validates it at startup, but worker/orchestrator spawns resolve from
	// explicit requests or project role config instead of falling back to it.
	DefaultAgent = "claude-code"
)

// GitLabConfig carries the self-managed GitLab host allowlist and per-host
// token overrides. It is loaded once at daemon boot from environment variables
// (no hot-reload), matching the existing config pattern. gitlab.com is always
// allowed (hardcoded in the provider) and does not need to appear here.
type GitLabConfig struct {
	// AllowedHosts is the list of self-managed GitLab hosts (each may include a
	// port, e.g. "gitlab.internal:8443"). gitlab.com is always allowed and is not
	// included here.
	AllowedHosts []string
	// HostTokens maps a self-managed host to a token override. Hosts in
	// AllowedHosts without an explicit entry fall back to the default token
	// (TALLPINE_GITLAB_TOKEN / GITLAB_TOKEN / glab).
	HostTokens map[string]string
}

// DefaultAllowedOrigins are the browser origins the daemon's CORS boundary
// trusts. Workspace previews and unrelated local servers are not trusted
// API clients. Native callers remain unauthenticated; paired mobile callers
// use the separately authenticated listener. Every default
// entry must be an origin ordinary web content cannot present:
// app://renderer is the packaged Electron renderer, served from a custom
// scheme only the desktop app registers — no website can bear it. The opaque
// "null" origin (file:// pages, sandboxed iframes on any website) must never
// be added.
var DefaultAllowedOrigins = []string{
	"app://renderer",
}

// Config is the fully-resolved daemon configuration. It is immutable once
// built by Load.
type Config struct {
	// Host is the bind address. Always loopback — see LoopbackHost.
	Host string
	// Port is the TCP port to bind. The daemon fails fast if it is taken.
	Port int
	// RequestTimeout bounds REST request handling.
	RequestTimeout time.Duration
	// SpawnTimeout is the base budget of the routes that run a project's Setup
	// (spawn, orchestrator start, restore) before Setup steps extend it. Zero
	// uses RequestTimeout.
	SpawnTimeout time.Duration
	// ShutdownTimeout is the hard graceful-shutdown deadline.
	ShutdownTimeout time.Duration
	// ArtifactVersionsKeep is how many versions of each session artifact file
	// the daemon keeps (TALLPINE_ARTIFACT_VERSIONS_KEEP, 1-1000).
	ArtifactVersionsKeep int
	// ArtifactSizeWarnBytes is the per-session artifact size at which the
	// storage report flags a session (TALLPINE_ARTIFACT_SIZE_WARN_MB, default 1024).
	ArtifactSizeWarnBytes int64
	// RunFilePath is where the PID + port handshake file (running.json) is
	// written so the Electron supervisor can discover and reap the daemon.
	RunFilePath string
	// LoopbackTCP keeps the 127.0.0.1:Port TCP listener open beside the
	// user-only Unix socket (TALLPINE_LOOPBACK_TCP=1). Off by default (B176):
	// the app, CLI and hooks reach the daemon over the socket, and Port is
	// then only the origin the desktop app names in daemon URLs. It is for
	// clients that cannot dial a Unix socket: an external browser opening a
	// preview, the browser-mode dev renderer, an agent's own HTTP client.
	// Without a socket (Windows, bind failure) the daemon listens on TCP anyway.
	LoopbackTCP bool
	// DaemonAuthDisabled turns off X-VO-Token enforcement on the loopback
	// listener (TALLPINE_DAEMON_AUTH=off). It is a debugging escape hatch only; see
	// docs/adr/0007-authenticated-loopback-listener.md.
	DaemonAuthDisabled bool
	// DaemonAuthOffIgnored records that TALLPINE_DAEMON_AUTH=off was set without the
	// TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1 second flag, so auth stays enforced.
	DaemonAuthOffIgnored bool
	// DataDir is the directory holding durable SQLite state: DB and WAL files.
	// It is created on first use by the storage layer.
	DataDir string
	// StateDir is the root for non-SQLite Tallpine state. It defaults to ~/.tallpine. When
	// TALLPINE_DATA_DIR is explicitly set, that override is also the state root so an
	// isolated daemon never leaks account state into the default home.
	StateDir string
	// Agent is the compatibility agent adapter id selected by TALLPINE_AGENT;
	// startSession fails fast if no adapter with this id is registered.
	Agent string
	// AppRunID identifies one desktop-app launch and scopes transient command
	// terminals to it. User shells survive across launches. The supervisor keeps
	// this constant across daemon restarts; bare daemons mint a fresh id per boot.
	AppRunID string
	// AllowedOrigins are the browser origins granted CORS read access (see
	// DefaultAllowedOrigins). Overridden by TALLPINE_ALLOWED_ORIGINS.
	AllowedOrigins []string
	// HarnessUpdateChecks lets the daemon compare installed harnesses with their
	// release sources over the network. TALLPINE_HARNESS_UPDATE_CHECKS=off disables
	// it for offline or policy-restricted machines.
	HarnessUpdateChecks bool
	// HarnessUpdateInterval is how often the daemon re-checks installed
	// harnesses after its startup check. TALLPINE_HARNESS_UPDATE_INTERVAL overrides
	// it; values below MinHarnessUpdateInterval are rejected.
	HarnessUpdateInterval time.Duration
	// UpdateWhileRunning lets a harness update run while the harness's
	// sessions stay open, when its installer keeps the running release usable
	// and the user confirms. TALLPINE_UPDATE_WHILE_RUNNING=off makes every update
	// wait for those sessions to end again.
	UpdateWhileRunning bool
	// VersionRefresh lets idle open sessions move to a harness release an
	// update installed while they ran. TALLPINE_VERSION_REFRESH=off is the kill
	// switch; the user's Settings toggle applies on top of it.
	VersionRefresh bool
	// VersionRefreshIdle is how long a session must be quiet before it moves,
	// so a warm prompt cache is not thrown away (TALLPINE_VERSION_REFRESH_IDLE).
	VersionRefreshIdle time.Duration
	// WorkerIdleNudge is how long a worker may sit idle after its turn, with
	// no report filed and no background job running, before Tallpine nudges it
	// (TALLPINE_WORKER_IDLE_NUDGE, a Go duration of at least a minute, or off).
	// Zero disables the nudge.
	WorkerIdleNudge time.Duration
	// ReviewPassDeadline is how long a Tallpine review pass may run without
	// submitting a result before Tallpine fails it (TALLPINE_REVIEW_PASS_DEADLINE, a Go
	// duration of at least ten minutes, or off). Zero disables the deadline.
	ReviewPassDeadline time.Duration
	// ReviewIdleGrace is how long a terminal reviewer may sit idle at its
	// prompt with a pass running before Tallpine fails that pass, for reviewers
	// that wait on long background work (TALLPINE_REVIEW_IDLE_GRACE, a Go duration
	// of at least a minute, or off). Zero disables the idle expiry; the pass
	// deadline still applies.
	ReviewIdleGrace time.Duration
	// TUIVersionRefresh also restarts idle terminal sessions on a newly
	// installed release (TALLPINE_TUI_VERSION_REFRESH=off disables it).
	TUIVersionRefresh bool
	// SessionGradleHome gives every session a per-project GRADLE_USER_HOME
	// under the data dir, so Gradle caches agents create can be removed with
	// the project (TALLPINE_SESSION_GRADLE_HOME=off keeps Gradle's own default).
	SessionGradleHome bool
	// UpdateWhenIdle lets a harness update that cannot run beside its sessions
	// wait until they are idle, then stop them, update and resume them,
	// instead of refusing while they run (TALLPINE_UPDATE_WHEN_IDLE=off disables
	// it; the older TALLPINE_WINDOWS_UPDATE_WHEN_IDLE is still read).
	UpdateWhenIdle bool
	// ManagedHarnessInstalls installs npm, Bun, uv and pipx harness updates
	// side by side under the Tallpine data dir and switches to them atomically, so
	// no update touches files a running session uses
	// (TALLPINE_MANAGED_HARNESS_INSTALLS=off updates the installed copy in place).
	ManagedHarnessInstalls bool
	// StartupWorkingDirectory is the daemon process cwd before startup
	// normalizes it. The desktop uses this to identify dev daemons after the
	// process cwd is moved to the stable data dir.
	StartupWorkingDirectory string
	// GitLab carries the self-managed GitLab host allowlist and per-host
	// token overrides, loaded once at boot from environment variables.
	GitLab GitLabConfig
	// GitHubEnterprise carries GHES hosts and per-host tokens (env only).
	GitHubEnterprise GitHubEnterpriseConfig
	// TrackerIntake gates the issue-intake observer daemon-wide
	// (TALLPINE_TRACKER_INTAKE).
	TrackerIntake bool
	// BacklogRun gates backlog run mode ("finish the backlog") daemon-wide
	// (TALLPINE_BACKLOG_RUN). On by default; TALLPINE_BACKLOG_RUN=off turns run mode off.
	BacklogRun bool
	// Planner gates the planner role and plan records (B205) daemon-wide
	// (TALLPINE_PLANNER). On by default; TALLPINE_PLANNER=off turns it off (a kill switch, not a product setting).
	Planner bool
	// Client identifies which client this deployment serves (TALLPINE_CLIENT). It is
	// reported to clients through the settings API and gates nothing in the
	// daemon.
	Client string
	// LocalOffering reports whether the local offering is available
	// (TALLPINE_LOCAL_OFFERING). It defaults to true; only an explicit false/0
	// switches it off.
	LocalOffering bool
	// AgentBrowserURL is the host's agent browser (TALLPINE_AGENT_BROWSER_URL): the
	// Chromium its sessions' agents drive, which clients show as the agent's
	// tab through /sessions/{id}/agent-browser. A loopback http URL; empty
	// means this host has none.
	AgentBrowserURL string
	// AgentBrowserToken is the agent browser's own credential
	// (TALLPINE_AGENT_BROWSER_TOKEN). The daemon adds it; clients never see it.
	AgentBrowserToken string
}

// Addr returns the host:port the HTTP server binds. It uses net.JoinHostPort so
// the result is correct for IPv6 literals as well as IPv4 / hostnames.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

// Load resolves configuration from the environment, applying defaults. It
// returns an error only for values that are present but malformed (e.g. a
// non-numeric TALLPINE_PORT); missing values fall back to defaults.
//
// Recognised variables:
//
//	TALLPINE_PORT              bind port           (default 3101)
//	TALLPINE_REQUEST_TIMEOUT   per-request timeout (Go duration > 0, default 60s)
//	TALLPINE_SHUTDOWN_TIMEOUT  shutdown deadline   (Go duration > 0, default 10s)
//	TALLPINE_WORKER_IDLE_NUDGE idle silent-worker nudge delay (Go duration >= 1m or off, default 10m)
//	TALLPINE_RUN_FILE          running.json path   (default ~/.tallpine/running.json)
//	TALLPINE_DATA_DIR          durable state dir   (default ~/.tallpine/data)
//	TALLPINE_DAEMON_AUTH       off disables loopback token auth (debugging only; default enforced),
//	                     honored only with TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1
//	TALLPINE_AGENT             compatibility agent id (default claude-code)
//	TALLPINE_APP_RUN_ID        desktop-app launch id, set by the Electron supervisor
//	                     (default: a fresh id minted per daemon boot)
//	TALLPINE_ALLOWED_ORIGINS   CORS origins, comma-separated (default DefaultAllowedOrigins)
//	TALLPINE_GITLAB_ALLOWED_HOSTS    comma-separated self-managed GitLab hosts (each may include :port)
//	TALLPINE_GITLAB_HOST_TOKENS      host=token,host=token per-host token overrides
//	TALLPINE_GITHUB_ENTERPRISE_HOSTS comma-separated self-hosted GHES hosts (each may include :port); *.ghe.com is built in
//	TALLPINE_GITHUB_HOST_TOKENS      host=token,host=token enterprise tokens (else gh auth token --hostname)
//	TALLPINE_CLIENT                  client identity reported to clients (trimmed, default empty)
//	TALLPINE_LOCAL_OFFERING          local offering off|on (default on)
//	TALLPINE_AGENT_BROWSER_URL       the host's agent browser (loopback http URL, default none)
//	TALLPINE_AGENT_BROWSER_TOKEN     that browser's credential (trimmed, required with TALLPINE_AGENT_BROWSER_URL)
//
// The bind host is not configurable: the daemon is loopback-only by design.
func Load() (Config, error) {
	cfg := Config{
		Host:                   LoopbackHost,
		Port:                   DefaultPort,
		RequestTimeout:         DefaultRequestTimeout,
		ShutdownTimeout:        DefaultShutdownTimeout,
		ArtifactVersionsKeep:   DefaultArtifactVersionsKeep,
		ArtifactSizeWarnBytes:  DefaultArtifactSizeWarnMB << 20,
		Agent:                  DefaultAgent,
		AllowedOrigins:         DefaultAllowedOrigins,
		LocalOffering:          true,
		HarnessUpdateChecks:    true,
		HarnessUpdateInterval:  DefaultHarnessUpdateInterval,
		UpdateWhileRunning:     true,
		VersionRefresh:         true,
		VersionRefreshIdle:     DefaultVersionRefreshIdle,
		WorkerIdleNudge:        DefaultWorkerIdleNudge,
		ReviewPassDeadline:     DefaultReviewPassDeadline,
		ReviewIdleGrace:        DefaultReviewIdleGrace,
		TUIVersionRefresh:      true,
		SessionGradleHome:      true,
		UpdateWhenIdle:         true,
		ManagedHarnessInstalls: true,
	}

	if err := cfg.loadLimits(); err != nil {
		return Config{}, err
	}
	if raw := brand.Getenv("AGENT"); raw != "" {
		cfg.Agent = raw
	}

	// A missing TALLPINE_APP_RUN_ID means nothing is supervising this daemon, so this
	// boot IS the run: mint an id rather than leaving it empty, which would make
	// every boot share one run id and defeat orphan detection entirely.
	if raw := brand.Getenv("APP_RUN_ID"); raw != "" {
		cfg.AppRunID = raw
	} else {
		cfg.AppRunID = newAppRunID()
	}

	if err := cfg.loadRuntimeToggles(); err != nil {
		return Config{}, err
	}
	if err := cfg.GitLab.loadEnv(); err != nil {
		return Config{}, err
	}
	ghe, err := LoadGitHubEnterprise()
	if err != nil {
		return Config{}, err
	}
	cfg.GitHubEnterprise = ghe

	if err := cfg.loadFeatureFlags(); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(brand.Getenv("AGENT_BROWSER_URL")); raw != "" {
		u, err := parseAgentBrowserURL(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.AgentBrowserURL = u
		cfg.AgentBrowserToken = strings.TrimSpace(brand.Getenv("AGENT_BROWSER_TOKEN"))
		if cfg.AgentBrowserToken == "" {
			return Config{}, fmt.Errorf("TALLPINE_AGENT_BROWSER_URL is set but TALLPINE_AGENT_BROWSER_TOKEN is empty: the agent browser refuses every request without its token")
		}
	}

	runFile, err := resolveRunFilePath()
	if err != nil {
		return Config{}, err
	}
	cfg.RunFilePath = runFile
	cfg.DaemonAuthDisabled = daemontoken.AuthOffHonored(os.Getenv(daemontoken.AuthEnv), os.Getenv(daemontoken.UnsafeDevEnv))
	cfg.DaemonAuthOffIgnored = !cfg.DaemonAuthDisabled && daemontoken.AuthDisabled(os.Getenv(daemontoken.AuthEnv))

	dataDir, err := resolveDataDir()
	if err != nil {
		return Config{}, err
	}
	cfg.DataDir = dataDir
	if raw, ok := brand.LookupEnv("DATA_DIR"); ok && raw != "" {
		cfg.StateDir = dataDir
	} else {
		cfg.StateDir = filepath.Dir(dataDir)
	}

	return cfg, nil
}

// ParseToggle parses an on/off environment value the way the daemon does
// (on|true|1|yes, off|false|0|no). The settings registry uses it to tell
// whether a kill-switch variable forces a setting.
func ParseToggle(raw string) (bool, error) { return parseToggleEnv("value", raw) }

func parseToggleEnv(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be off|on", name)
	}
}

// parseHostTokenMap parses a host=token,host=token map. Whitespace around
// entries, hosts, and tokens is trimmed. Empty entries and entries without an
// equals sign are skipped. A token containing an equals sign is rejected as
// ambiguous (a token value with embedded '=' would be indistinguishable from
// a malformed entry).
func parseHostTokenMap(name, raw string) (map[string]string, error) {
	tokens := make(map[string]string, 4)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue // skip entries without an equals sign
		}
		host := strings.TrimSpace(entry[:eq])
		token := strings.TrimSpace(entry[eq+1:])
		if host == "" {
			continue
		}
		// Reject tokens containing '=' — they would be ambiguous on re-parse
		// and likely indicate a malformed entry (e.g. host=token=with=equals).
		if strings.ContainsRune(token, '=') {
			return nil, fmt.Errorf("invalid %s entry %q: token contains '='", name, entry)
		}
		tokens[host] = token
	}
	return tokens, nil
}

// parseAgentBrowserURL accepts only a loopback http URL: the daemon proxies
// to it with clients' requests, so it must never reach another machine.
func parseAgentBrowserURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid TALLPINE_AGENT_BROWSER_URL %q: %w", raw, err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || !loopback {
		return "", fmt.Errorf("invalid TALLPINE_AGENT_BROWSER_URL %q: must be a loopback http URL without credentials or query", raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

// parsePositiveDuration rejects zero and negative durations: a zero
// RequestTimeout would expire every request instantly, and a non-positive
// ShutdownTimeout would defeat graceful shutdown.
// parseOffOrDuration reads a setting such as TALLPINE_WORKER_IDLE_NUDGE: off (or 0)
// disables it, anything else is a duration of at least minimum.
func parseOffOrDuration(name, raw string, minimum time.Duration) (time.Duration, error) {
	if strings.EqualFold(raw, "off") || raw == "0" {
		return 0, nil
	}
	d, err := parsePositiveDuration(name, raw)
	if err != nil {
		return 0, err
	}
	if d < minimum {
		return 0, fmt.Errorf("invalid %s %q: must be off or at least %s", name, raw, minimum)
	}
	return d, nil
}

func parsePositiveDuration(name, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be > 0", name, raw)
	}
	return d, nil
}

// newAppRunID mints the fallback launch id used when no supervising app
// supplied one. Randomness (not a timestamp or PID) is what guarantees two
// boots never collide, which is what orphan detection relies on. A failure to
// read entropy falls back to the boot time — worse, but still monotonic enough
// to distinguish runs, and never worth refusing to start the daemon over.
func newAppRunID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "apprun-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "apprun-" + hex.EncodeToString(buf)
}

// resolveRunFilePath picks where running.json lives. An explicit TALLPINE_RUN_FILE
// wins; otherwise it sits under the canonical Tallpine home directory so the CLI and
// Electron supervisor share one handshake location.
func resolveRunFilePath() (string, error) {
	if p, ok := brand.LookupEnv("RUN_FILE"); ok && p != "" {
		return absOverride("TALLPINE_RUN_FILE", p)
	}
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "running.json"), nil
}

// DataDirFromEnv returns the explicit TALLPINE_DATA_DIR override, absolutized
// against the launch cwd (see absOverride), and whether one is set. A blank
// value counts as unset.
func DataDirFromEnv() (string, bool) {
	p, ok := brand.LookupEnv("DATA_DIR")
	p = strings.TrimSpace(p)
	if !ok || p == "" {
		return "", false
	}
	abs, err := absOverride("TALLPINE_DATA_DIR", p)
	if err != nil {
		return p, true
	}
	return abs, true
}

// DefaultDataDir is ~/.tallpine/data, or "" when the home directory cannot be
// resolved.
func DefaultDataDir() string {
	dir, err := defaultDataDir()
	if err != nil {
		return ""
	}
	return dir
}

func defaultDataDir() (string, error) {
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "data"), nil
}

// resolveDataDir picks where durable state (the SQLite DB) lives. An explicit
// TALLPINE_DATA_DIR wins; otherwise it defaults under the same canonical Tallpine home
// directory as the run-file. It is the one resolver: DataDirFromEnv and
// DefaultDataDir are its two halves.
func resolveDataDir() (string, error) {
	if p, ok := DataDirFromEnv(); ok {
		return p, nil
	}
	return defaultDataDir()
}

func defaultStateDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return filepath.Join(homeDir, brand.HomeDirName), nil
}

// absOverride resolves an explicit TALLPINE_DATA_DIR/TALLPINE_RUN_FILE override to an
// absolute path against the process's launch cwd. The daemon chdir's into its
// data dir at startup (see stabilizeWorkingDirectory), so a relative override
// left as-is would be re-resolved against the new cwd and double-nest state
// (e.g. TALLPINE_DATA_DIR=data -> <cwd>/data/data). Absolutizing here keeps the path
// stable regardless of the later chdir.
func absOverride(name, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", name, p, err)
	}
	return abs, nil
}
