package domain

import "fmt"

// AgentHarness identifies which agent CLI/runtime a session drives.
type AgentHarness string

// Supported agent harnesses.
const (
	HarnessClaudeCode AgentHarness = "claude-code"
	HarnessCodex      AgentHarness = "codex"
	HarnessKiro       AgentHarness = "kiro"
	HarnessDeepSeek   AgentHarness = "deepseek-harness"
	// HarnessFake is retained for existing test fixtures and historical session
	// rows, but is not user-selectable.
	HarnessFake AgentHarness = "fake"
)

// AllHarnesses lists every supported harness. It is the canonical set used to
// validate user-supplied harness names (e.g. per-project role overrides).
var AllHarnesses = []AgentHarness{
	HarnessClaudeCode, HarnessCodex, HarnessKiro, HarnessDeepSeek,
}

// IsKnown reports whether h is one of the supported harnesses.
func (h AgentHarness) IsKnown() bool {
	for _, k := range AllHarnesses {
		if h == k {
			return true
		}
	}
	return false
}

// FallbackHarness is what stored configuration that names an unsupported
// harness resolves to. Harnesses other than claude-code, codex, kiro and deepseek-harness were
// removed, but projects, automations and daemon defaults saved earlier can
// still name them.
const FallbackHarness = HarnessClaudeCode

// removedHarnessNames lists every harness id that earlier builds shipped (as a
// worker or reviewer) and this one does not. Only these are tolerated, with a
// fallback, when they turn up in stored or daemon configuration being set; any
// other unknown id is a typo and still fails validation.
var removedHarnessNames = map[string]bool{
	"aider": true, "opencode": true, "opencode-v2": true, "grok": true, "droid": true, "amp": true,
	"agy": true, "crush": true, "cursor": true, "qwen": true, "gemini": true, "copilot": true,
	"goose": true, "auggie": true, "continue": true, "devin": true, "cline": true, "kimi": true,
	"muse": true, "kilocode": true, "vibe": true, "pi": true, "kimchi": true, "prime-agent": true,
	"autohand": true, "omp": true, "fx": true, "unreal-agent": true, "codewhale": true,
	"mimo-code": true, "memcode": true, "openhands": true,
	"command-code": true, "hermes": true,
}

// IsRemoved reports whether h is the id of a harness that earlier builds
// shipped and this one no longer does. Unlike IsUnsupported it is false for an
// id that never existed, so a typo is not mistaken for a removed harness.
func (h AgentHarness) IsRemoved() bool { return removedHarnessNames[string(h)] }

// IsUnsupported reports whether h names a harness this build no longer ships.
// The empty harness ("not pinned") and the test-only fake are not unsupported.
func (h AgentHarness) IsUnsupported() bool {
	return h != "" && h != HarnessFake && !h.IsKnown()
}

// OrFallback resolves a harness read from stored configuration. An empty or
// supported harness is returned unchanged; an unsupported one resolves to
// FallbackHarness and replaced is true so the caller can log the substitution.
func (h AgentHarness) OrFallback() (resolved AgentHarness, replaced bool) {
	if h.IsUnsupported() {
		return FallbackHarness, true
	}
	return h, false
}

// UnsupportedHarnessMessage is the user-facing explanation for a session whose
// harness was removed. Such a session stays visible but is read-only.
func UnsupportedHarnessMessage(h AgentHarness) string {
	return fmt.Sprintf("Harness %q is no longer supported by this version of Tallpine. This session is read-only: terminate it, or start a new session with claude-code, codex or kiro.", string(h))
}
