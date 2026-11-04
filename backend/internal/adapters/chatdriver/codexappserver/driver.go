package codexappserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// clientName identifies Tallpine to the provider. It shows up in the app-server's
// reported user agent, which makes a stray process attributable.
const (
	clientName    = "tallpine"
	clientTitle   = "Tallpine"
	clientVersion = "0.1.0"
	// This is the oldest Codex build whose complete Chat surface Tallpine exercised:
	// thread start/resume, turn start/interrupt, approvals, and every advertised
	// extension. initialize plus model/list alone cannot prove those mutating
	// methods without creating provider state during preflight.
	minimumCodexVersion = "0.146.0"
)

// handshakeTimeout bounds initialize and thread open. These are local IPC calls
// that normally settle in well under a second.
const handshakeTimeout = 60 * time.Second

// codexPlugin is the subset of Tallpine's existing Codex agent plugin that the Chat
// driver reuses. Binary resolution and local auth probing already live there and
// must not be reimplemented: a second copy would drift from what TUI sessions do.
type codexPlugin interface {
	ResolveBinary(ctx context.Context) (string, error)
	AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error)
}

// process is a running app-server, abstracted so tests can substitute pipes for
// a child process.
type process struct {
	stdin  io.WriteCloser
	stdout io.Reader
	// reconnected means the provider process and initialized protocol connection
	// survived a prior daemon. The replacement must not initialize/resume it a
	// second time.
	reconnected   bool
	nextRequestID int64
	// stop releases the process. It must be safe to call more than once.
	stop func() error
	// terminate destroys a persistent host for explicit session shutdown.
	terminate func() error
}

// spawnFunc launches an app-server. Injected so tests never exec anything.
type spawnFunc func(ctx context.Context, bin, workdir string, env []string) (*process, error)

type versionProbeFunc func(context.Context, string) (string, error)

type persistentConnectFunc func(context.Context, persistenthost.Config) (*persistenthost.Transport, error)

type fixedCodexPlugin string

func (p fixedCodexPlugin) ResolveBinary(context.Context) (string, error) { return string(p), nil }

func (fixedCodexPlugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnknown, nil
}

// Driver opens Codex conversations over `codex app-server`.
type Driver struct {
	plugin       codexPlugin
	log          *slog.Logger
	spawn        spawnFunc
	versionProbe versionProbeFunc
	persistent   bool
	connectHost  persistentConnectFunc
}

// New builds a Chat driver over the existing Codex agent plugin.
func New(plugin codexPlugin, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Driver{
		plugin: plugin, log: log, spawn: spawnAppServer,
		versionProbe: installedCodexVersion, persistent: true, connectHost: persistenthost.ConnectOrStart,
	}
}

// DiscoverModels performs the same bounded app-server model/list read as a live
// conversation without creating a provider thread.
func DiscoverModels(ctx context.Context, binary, workdir string, env map[string]string) ([]ports.ChatModel, error) {
	driver := New(fixedCodexPlugin(binary), slog.New(slog.DiscardHandler))
	conv, err := driver.connect(ctx, workdir, env, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()
	return conv.ListModels(ctx)
}

var _ ports.ChatDriver = (*Driver)(nil)

// Harness reports which agent this driver serves.
func (d *Driver) Harness() domain.AgentHarness { return domain.HarnessCodex }

// capabilities is what a Codex app-server of a supported version provides. Each
// entry here was exercised against a live app-server rather than read off a doc.
func capabilities() ports.ChatCapabilities {
	return ports.ChatCapabilities{
		ports.ChatCapabilityStreaming:   true,
		ports.ChatCapabilityTools:       true,
		ports.ChatCapabilityApprovals:   true,
		ports.ChatCapabilityInterrupt:   true,
		ports.ChatCapabilityResume:      true,
		ports.ChatCapabilityHistory:     true,
		ports.ChatCapabilityUsage:       true,
		ports.ChatCapabilityDiffs:       true,
		ports.ChatCapabilityPlans:       true,
		ports.ChatCapabilityPlanMode:    true,
		ports.ChatCapabilityInteractive: true,
		ports.ChatCapabilityModels:      true,
		// The account's quota position is both pushed (account/rateLimits/updated)
		// and readable on demand (account/rateLimits/read), verified against a live
		// account.
		ports.ChatCapabilityRateLimits: true,
		// Without this a long conversation eventually cannot accept another turn at
		// all: every turn re-sends the history, so context fills on its own and the
		// only way back is to summarize what is already there.
		ports.ChatCapabilityCompaction: true,
		// History operations, all three exercised against a live app-server. Rollback
		// is advertised despite thread/rollback carrying a DEPRECATED annotation:
		// what the installed provider does is the only honest answer, and gating the
		// feature off while the call still works would take undo away for no reason.
		ports.ChatCapabilityRollback: true,
		ports.ChatCapabilityFork:     true,
		ports.ChatCapabilityRename:   true,
		ports.ChatCapabilitySkills:   true,
		// config/mcpServer/reload plus the status inventory read after it, both
		// exercised against a live app-server.
		ports.ChatCapabilityMCPReload: true,
		// Guidance into a turn already in flight, over turn/steer. Advertised only
		// after being driven against a live app-server (TestLiveSteerKeepsTheTurnAndItsWork
		// on codex-cli 0.146.0): the steered turn kept its id, emitted one
		// turn/started and one turn/completed, settled `completed` rather than
		// interrupted, and followed the correction. Strictly better than
		// interrupt-and-resend, which throws the turn's context and in-flight work
		// away.
		ports.ChatCapabilitySteer: true,
	}
}

// Probe reports what this install can do without creating a conversation, so an
// unsupported request can be refused before Tallpine commits a session or worktree.
func (d *Driver) Probe(ctx context.Context) (ports.ChatCapabilities, error) {
	bin, err := d.plugin.ResolveBinary(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}

	// Authentication is owned by the daemon's active-account readiness check.
	// Probing the ambient device home here would reject a valid Tallpine account (or
	// admit a different device account) before the managed runtime is launched.
	versionProbe := d.versionProbe
	if versionProbe == nil {
		versionProbe = installedCodexVersion
	}
	versionCtx, versionCancel := context.WithTimeout(ctx, 5*time.Second)
	versionOutput, versionErr := versionProbe(versionCtx, bin)
	versionCancel()
	if versionErr != nil {
		return nil, fmt.Errorf("%w: read Codex version: %w", ports.ErrChatDriverIncompatible, versionErr)
	}
	installed, ok := parseCodexVersion(versionOutput)
	if !ok {
		return nil, fmt.Errorf("%w: unrecognized Codex version %q (Tallpine requires %s or newer)",
			ports.ErrChatDriverIncompatible, strings.TrimSpace(versionOutput), minimumCodexVersion)
	}
	minimum, _ := parseCodexVersion(minimumCodexVersion)
	if installed.less(minimum) {
		return nil, fmt.Errorf("%w: Codex %s is older than Tallpine's tested minimum %s",
			ports.ErrChatDriverIncompatible, installed, minimumCodexVersion)
	}

	// Binary presence is not protocol compatibility. Complete the same initialize
	// handshake a real controller uses, then exercise model/list: it is part of
	// the surface Tallpine advertises and a harmless read that catches older app-server
	// builds before a session row or worktree exists.
	workdir, err := os.Getwd()
	if err != nil || !filepath.IsAbs(workdir) {
		workdir = os.TempDir()
	}
	probeCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	conv, err := d.connect(probeCtx, workdir, nil, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()
	var models struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := conv.conn.request(probeCtx, "model/list", map[string]any{}, &models); err != nil {
		return nil, fmt.Errorf("%w: model/list: %w", ports.ErrChatDriverIncompatible, err)
	}

	return capabilities(), nil
}

// DiscoverModels reads the account's current provider catalog without opening
// a Codex thread. The caller supplies the same project directory and environment
// overlay used for a normal launch so project-scoped Codex configuration applies.
func (d *Driver) DiscoverModels(ctx context.Context, workdir string, env map[string]string) ([]ports.ChatModel, error) {
	if !filepath.IsAbs(workdir) {
		var err error
		workdir, err = os.Getwd()
		if err != nil || !filepath.IsAbs(workdir) {
			workdir = os.TempDir()
		}
	}
	conv, err := d.connect(ctx, workdir, env, "")
	if err != nil {
		return nil, err
	}
	defer func() { _ = conv.Close() }()
	return listModels(ctx, conv.conn)
}

type codexVersion [3]int

var codexVersionPattern = regexp.MustCompile(`\b(\d+)\.(\d+)\.(\d+)\b`)

func parseCodexVersion(output string) (codexVersion, bool) {
	match := codexVersionPattern.FindStringSubmatch(output)
	if len(match) != 4 {
		return codexVersion{}, false
	}
	var version codexVersion
	for i := range version {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return codexVersion{}, false
		}
		version[i] = value
	}
	return version, true
}

func (v codexVersion) less(other codexVersion) bool {
	for i := range v {
		if v[i] != other[i] {
			return v[i] < other[i]
		}
	}
	return false
}

func (v codexVersion) String() string {
	return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
}

func installedCodexVersion(ctx context.Context, bin string) (string, error) {
	cmd := tallpineprocess.CommandContext(ctx, bin, "--version")
	cmd.Env = codexProcessEnv(ctx, bin, nil)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
