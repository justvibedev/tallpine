// Package modelcatalog normalizes the heterogeneous model-list surfaces
// exposed by supported agent CLIs.
package modelcatalog

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/authprobe"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	// Model-list commands may initialize provider registries or refresh remote
	// metadata before printing their catalog. Kilo Code and OpenCode can exceed
	// eight seconds on a normal warm installation, so keep one consistent,
	// bounded allowance for every command-backed adapter.
	commandTimeout = 20 * time.Second

	// Some CLIs leave descendants holding stdout/stderr open after the parent is
	// canceled. Bound that pipe-drain wait so a timed-out discovery request does
	// not linger well beyond commandTimeout.
	commandTerminationWait = 2 * time.Second
)

type commandSpec struct {
	args   []string
	parser func([]byte) ([]ports.AgentModelInfo, error)
	// signIn is set for CLIs whose model-list command opens an interactive
	// browser sign-in when signed out. Discovery only runs once it confirms a
	// login, so background refreshes never open that sign-in page.
	signIn *signInCheck
}

// signInCheck confirms a login before a sign-in-prompting model command runs.
type signInCheck struct {
	// args is the CLI's non-interactive auth status command.
	args []string
	// envKeys are credentials that authorize the CLI without an interactive login.
	envKeys []string
}

// kiroSignIn uses Kiro's documented auth status command; `chat --list-models`
// opens the browser sign-in page when Kiro is signed out.
var kiroSignIn = &signInCheck{args: []string{"whoami", "--format", "json"}, envKeys: []string{"KIRO_API_KEY"}}

// signInCheckTimeout bounds the status probe; tests shorten it.
var signInCheckTimeout = 5 * time.Second

// signInProbe runs the status command; tests replace it.
var signInProbe = func(ctx context.Context, binary string, args []string, workingDir string, env map[string]string) ([]byte, error) {
	return modelCommand(ctx, binary, args, workingDir, env).CombinedOutput()
}

// check returns nil when the model command may run. A clear signed-out answer
// returns ErrAgentModelDiscoverySignInRequired. A cancellation returns the
// context error, and an inconclusive probe (timeout, or a failed exit without
// recognizable status text) returns an ordinary discovery error so the caller
// records and retries it. The model command never runs unless check returns nil.
func (c *signInCheck) check(ctx context.Context, agentID, binary, workingDir string, env map[string]string) error {
	for _, key := range c.envKeys {
		if strings.TrimSpace(env[key]) != "" || strings.TrimSpace(os.Getenv(key)) != "" {
			return nil
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, signInCheckTimeout)
	defer cancel()
	output, err := signInProbe(probeCtx, binary, c.args, workingDir, env)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s sign-in check canceled: %w", agentID, ctxErr)
	}
	if probeCtx.Err() != nil {
		return fmt.Errorf("%s sign-in check timed out after %s", agentID, signInCheckTimeout)
	}
	switch authprobe.StatusFromText(string(output)) {
	case ports.AgentAuthStatusAuthorized:
		return nil
	case ports.AgentAuthStatusUnauthorized:
		return fmt.Errorf("%s model discovery: %w", agentID, ports.ErrAgentModelDiscoverySignInRequired)
	}
	if err != nil {
		return fmt.Errorf("%s sign-in check failed: %w", agentID, err)
	}
	return nil
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[[:alpha:]]`)

var commandSpecs = map[string]commandSpec{
	"kiro": {args: []string{"chat", "--list-models", "--format", "json"}, parser: parseJSONModels, signIn: kiroSignIn},
}

// Accepts reports whether model may launch agentID. It rejects a model only
// when Tallpine owns a complete static catalog that does not list it; dynamic
// catalogs and direct-entry agents defer validation to the agent at launch.
func Accepts(agentID, model string) bool {
	if strings.TrimSpace(model) == "" {
		return true
	}
	catalog := Base(agentID)
	if catalog.AllowCustom || len(catalog.Models) == 0 {
		return true
	}
	for _, item := range catalog.Models {
		if strings.EqualFold(item.ID, model) {
			return true
		}
	}
	return false
}

// Base returns the picker behavior Tallpine can provide without executing a CLI.
func Base(agentID string) ports.AgentModelCatalog {
	now := time.Now().UTC()
	entryMode := customModelEntryMode(agentID)
	if hasDiscoverySource(agentID) {
		return ports.AgentModelCatalog{
			AgentID:          agentID,
			SelectionMode:    ports.ModelSelectionCatalog,
			Models:           []ports.AgentModelInfo{},
			CustomModelEntry: entryMode,
			AllowCustom:      entryMode == ports.CustomModelEntryDirect,
			Source:           "cli",
			FetchedAt:        now,
		}
	}
	return Manual(agentID)

}

// Manual returns the capability-aware fallback used when an adapter has no
// reliable catalog or discovery fails before Tallpine has a successful cache.
func Manual(agentID string) ports.AgentModelCatalog {
	entryMode := customModelEntryMode(agentID)
	selectionMode := ports.ModelSelectionCatalog
	if entryMode == ports.CustomModelEntryDirect {
		selectionMode = ports.ModelSelectionText
	}
	return ports.AgentModelCatalog{
		AgentID:          agentID,
		SelectionMode:    selectionMode,
		Models:           []ports.AgentModelInfo{},
		CustomModelEntry: entryMode,
		AllowCustom:      entryMode == ports.CustomModelEntryDirect,
		Source:           "manual",
		FetchedAt:        time.Now().UTC(),
	}
}

// customModelEntryMode is stable adapter capability policy. Model names and
// availability remain agent-owned and are never listed here.
func customModelEntryMode(agentID string) ports.CustomModelEntryMode {
	switch agentID {
	case "claude-code", "codex":
		return ports.CustomModelEntryDirect
	default:
		return ports.CustomModelEntryNone
	}
}

// Discoverer implements the model-discovery port for production daemon wiring.
type Discoverer struct {
	CodexModels       CodexModelListFunc
	ACPOptions        map[string]ACPOptionListFunc
	ClaudeModels      ClaudeModelListFunc
	ClaudeFingerprint ClaudeFingerprintFunc
}

// CodexModelListFunc obtains Codex's account-scoped app-server catalog without
// opening a provider thread.
type CodexModelListFunc func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatModel, error)

// ACPOptionListFunc obtains a harness's provider-owned model choices from the
// ACP configuration catalog advertised by session/new. It is keyed by harness id
// because each agent reaches that catalog through its own command line, and the
// value a session accepts is exactly the value the catalog advertised.
type ACPOptionListFunc func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.ChatConfigOption, error)

// ClaudeModelListFunc obtains the Claude model IDs the configured provider
// actually serves, in that provider's own ID format. It returns an error
// whenever the provider could not be asked; the service then retains its
// matching cached catalog instead of replacing it with guessed models.
type ClaudeModelListFunc func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error)

// ClaudeFingerprintFunc supplies account/provider identity that is not present
// in settings files, such as subscription credentials and apiProvider.
type ClaudeFingerprintFunc func(context.Context, ports.AgentModelDiscoveryRequest) string

// Discover uses the agent-owned model surface configured for this adapter.
func (d Discoverer) Discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	return d.discover(ctx, request)
}

func (d Discoverer) discover(ctx context.Context, request ports.AgentModelDiscoveryRequest) (ports.AgentModelCatalog, error) {
	if request.AgentID == "claude-code" {
		return discoverClaudeCatalog(ctx, request, d.ClaudeModels)
	}
	if request.AgentID == "codex" {
		return discoverCodexCatalog(ctx, request, d.CodexModels)
	}
	if list := d.ACPOptions[request.AgentID]; list != nil {
		// An ACP-only harness has no command spec to fall back to, so a failure
		// must surface: an empty catalog would be recorded as a successful
		// discovery and park the picker until the next day.
		return discoverACPOptionCatalog(ctx, request, list)
	}
	return discoverCommandCatalog(ctx, request.AgentID, request.Binary, request.WorkingDir, request.Env)
}
