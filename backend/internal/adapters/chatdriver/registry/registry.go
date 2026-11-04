// Package registry resolves the Chat driver for a harness.
//
// Registration is the whole capability gate: a harness with no driver here cannot
// run in chat mode, and asking for one fails with ErrChatUnsupported rather than
// quietly producing a TUI session. Adding a harness to chat means adding it here,
// which keeps "which agents support chat" answerable by reading one file instead
// of inferring it from behavior.
package registry

import (
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/claudeacp"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/deepseekharnessacp"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/kiroacp"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Registry maps a harness to its Chat driver.
type Registry struct {
	drivers map[domain.AgentHarness]ports.ChatDriver
	// plugins are the drivers' own agent plugins, each caching the executable
	// it resolved.
	plugins map[domain.AgentHarness]ports.AgentBinaryResolutionInvalidator
}

var _ ports.ChatDriverRegistry = (*Registry)(nil)

// New builds a registry from an explicit driver list. Tests use it to register a
// fake without touching the shipped set.
func New(drivers ...ports.ChatDriver) *Registry {
	byHarness := make(map[domain.AgentHarness]ports.ChatDriver, len(drivers))
	for _, driver := range drivers {
		if driver == nil {
			continue
		}
		byHarness[driver.Harness()] = driver
	}
	return &Registry{drivers: byHarness}
}

// Build returns the drivers the daemon ships.
//
// Codex uses its native app-server protocol. Claude Code uses Tallpine's reusable ACP
// transport plus claude-agent-acp, pointed at the user's own Claude executable.
// No path scrapes terminal output or packages a second provider CLI.
//
// DeepSeek Harness runs `dsh --profile acp` through the same ACP transport via
// the shared ACP driver. Kiro runs `kiro-cli acp --agent-engine v3` the same way. Every other harness stays TUI-only. Each driver
// reuses the harness's existing agent plugin for binary resolution and auth, so
// registration adds no second answer to "is this agent installed and logged in".
func Build(log *slog.Logger, onClaudeAuthRejected func()) *Registry {
	r := New()
	r.plugins = make(map[domain.AgentHarness]ports.AgentBinaryResolutionInvalidator)
	add := func(driver ports.ChatDriver, plugin any) {
		r.drivers[driver.Harness()] = driver
		if invalidator, ok := plugin.(ports.AgentBinaryResolutionInvalidator); ok {
			r.plugins[driver.Harness()] = invalidator
		}
	}
	codexPlugin := codex.New()
	add(codexappserver.New(codexPlugin, log), codexPlugin)
	claudePlugin := claudecode.New()
	add(claudeacp.New(claudePlugin, log, onClaudeAuthRejected), claudePlugin)
	deepseekPlugin := deepseekharness.New()
	add(deepseekharnessacp.New(deepseekPlugin, log), deepseekPlugin)
	kiroPlugin := kiro.New()
	add(kiroacp.New(kiroPlugin, log), kiroPlugin)
	return r
}

// InvalidateBinaryResolution makes harness's driver resolve its executable
// again, after an install changed which one sessions should run.
func (r *Registry) InvalidateBinaryResolution(harness domain.AgentHarness) {
	if plugin, ok := r.plugins[harness]; ok {
		plugin.InvalidateBinaryResolution()
	}
}

// Driver returns the driver for a harness.
func (r *Registry) Driver(harness domain.AgentHarness) (ports.ChatDriver, error) {
	driver, ok := r.drivers[harness]
	if !ok {
		return nil, ports.ErrChatUnsupported
	}
	return driver, nil
}

// SupportsChat reports whether a harness has a driver at all, without probing the
// local install. Use it to decide whether Chat is even offerable; use Driver plus
// Probe to decide whether it will work right now.
func (r *Registry) SupportsChat(harness domain.AgentHarness) bool {
	_, ok := r.drivers[harness]
	return ok
}

// Harnesses lists the harnesses with a registered driver, for diagnostics and for
// telling a user which agents can run in chat mode.
func (r *Registry) Harnesses() []domain.AgentHarness {
	out := make([]domain.AgentHarness, 0, len(r.drivers))
	for harness := range r.drivers {
		out = append(out, harness)
	}
	return out
}
