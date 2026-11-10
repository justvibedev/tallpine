package domain

import (
	"fmt"
	"strconv"
	"strings"
)

// PermissionMode controls how much review an agent requires before acting. It
// lives in domain (not ports) so the typed AgentConfig can carry it; ports
// re-exports it as a type alias so agent adapters keep referring to
// ports.PermissionMode unchanged.
type PermissionMode string

// The permission modes adapters map onto their agent's native approval flags.
const (
	// PermissionModeDefault is special: adapters choose their own baseline
	// behavior for it. Most defer to the agent's own config; some managed
	// adapters may map it to a safer non-interactive default.
	PermissionModeDefault           PermissionMode = "default"
	PermissionModeAcceptEdits       PermissionMode = "accept-edits"
	PermissionModeAuto              PermissionMode = "auto"
	PermissionModeBypassPermissions PermissionMode = "bypass-permissions"
)

// AgentConfig is the typed per-project agent configuration. It replaces the
// former free-form map so the fields are validated and the API/UI render a
// real form rather than arbitrary JSON. An empty value (IsZero) means unset.
type AgentConfig struct {
	// Model overrides the agent's default model (e.g. claude-opus-4-5).
	Model string `json:"model,omitempty"`
	// Effort selects a model-advertised reasoning level. Empty defers to the
	// provider/model default.
	Effort string `json:"effort,omitempty"`
	// Mode selects an agent-owned operating mode when the adapter exposes modes
	// instead of raw model ids (currently Amp: low|medium|high|ultra).
	Mode string `json:"mode,omitempty"`
	// Permissions sets the agent's starting permission mode. Empty inherits the
	// project/role preference; new sessions fall back to Auto when none is saved.
	// Other adapter callers retain their existing baseline for an empty value.
	Permissions PermissionMode `json:"permissions,omitempty"`
	// Launch carries one session's own launch choices to its adapter. It is
	// never part of a saved or served project config.
	Launch AgentLaunchOptions `json:"-"`
}

// AgentLaunchOptions are choices a terminal agent only reads when it starts.
// Tallpine stores them with the session so a later restart launches it the same way.
type AgentLaunchOptions struct {
	// FastMode starts the agent on its faster tier (Codex).
	FastMode bool `json:"fastMode,omitempty"`
	// FastModeOff starts Codex on its standard tier even where its config or the
	// model would pick the faster one, as a switch whose picker showed it off asks.
	FastModeOff bool `json:"fastModeOff,omitempty"`
	// Ultracode starts Claude Code with Ultracode on.
	Ultracode bool `json:"ultracode,omitempty"`
	// AutoCompactWindow is where the agent compacts on its own: "120k" (any 50k to 1m), "1m", or
	// a percent of the model's window such as "70%" (see AutoCompactTokenCount).
	AutoCompactWindow string `json:"autoCompactWindow,omitempty"`
	// ModelContextWindow is the model's context window in tokens, 0 when unknown.
	// The session manager fills it at launch so a percent window can become a
	// token count; it is not stored.
	ModelContextWindow int64 `json:"-"`
	// EnabledPlugins is the Claude Code enabledPlugins override from the
	// project's claudePlugins setting for the session's role, as a JSON object
	// (a string keeps the options comparable). Filled at launch, not stored;
	// empty leaves the account's plugins unchanged.
	EnabledPlugins string `json:"-"`
	// AutoMemoryOff starts Claude Code with auto-memory off (the project's
	// claudeAutoMemory setting for the session's role). Filled at launch.
	AutoMemoryOff bool `json:"-"`
	// ShellOutputChars caps the shell output the agent reads inline (see
	// ShellOutputLimit). Filled at launch; 0 leaves the harness default.
	ShellOutputChars int `json:"-"`
	// HandoffNote is a terminal switch target's note, kept until the target has
	// started. A relaunch that finds it follows a switch cut short by a daemon
	// stop or crash, and delivers the note in place of the session's task.
	HandoffNote string `json:"handoffNote,omitempty"`
}

// AutoCompactTokensMin and AutoCompactTokensMax bound an explicit token-count
// choice such as "120k" or "1m" (B374: orchestrators compact well below 200k).
const (
	AutoCompactTokensMin = 50_000
	AutoCompactTokensMax = 1_000_000
)

// ParseAutoCompactTokens returns the token count of a stored "<N>k" or "1m"
// choice (50k to 1m), and false for anything else or a size out of range.
func ParseAutoCompactTokens(value string) (int, bool) {
	if value == "1m" {
		return AutoCompactTokensMax, true
	}
	digits, ok := strings.CutSuffix(value, "k")
	if !ok || digits == "" || len(digits) > 4 {
		return 0, false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n*1000 < AutoCompactTokensMin || n*1000 > AutoCompactTokensMax {
		return 0, false
	}
	return n * 1000, true
}

// AutoCompactPercentMin and AutoCompactPercentMax bound a percent-of-window
// choice, stored as a whole percent ("40%"). The Settings and popover pickers
// offer common steps, but any whole percent in range is accepted.
const (
	AutoCompactPercentMin = 20
	AutoCompactPercentMax = 95
)

// ParseAutoCompactPercent returns the whole percent of a stored "NN%" choice,
// and false for anything else or a percent outside the allowed range.
func ParseAutoCompactPercent(value string) (int, bool) {
	digits, ok := strings.CutSuffix(value, "%")
	if !ok || digits == "" || len(digits) > 2 {
		return 0, false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	percent, err := strconv.Atoi(digits)
	if err != nil || percent < AutoCompactPercentMin || percent > AutoCompactPercentMax {
		return 0, false
	}
	return percent, true
}

// Claude Code accepts a compaction window of 100k to 1M tokens
// (CLAUDE_CODE_AUTO_COMPACT_WINDOW in its env-vars reference).
const (
	autoCompactMinTokens = 100_000
	autoCompactMaxTokens = AutoCompactTokensMax
)

// ValidAutoCompactWindow reports whether the value is empty (Auto), a token
// preset, or a whole percent from 20 to 95.
func ValidAutoCompactWindow(value string) bool {
	if _, ok := ParseAutoCompactTokens(value); value == "" || ok {
		return true
	}
	_, ok := ParseAutoCompactPercent(value)
	return ok
}

// AutoCompactTokenCount is the compaction threshold in tokens for a stored
// choice, or 0 when Tallpine should leave the agent's own default: Auto, an
// unrecognised value, or a percent whose model window is unknown (never
// guessed). An explicit size or a percent is clamped to the range Claude Code accepts (a
// "60k" window runs as 100k), and this is
// the one place the clamp lives: 40% of a 200k model is 80k, raised to the 100k
// minimum (50% of that window), because Claude Code ignores a lower value.
func AutoCompactTokenCount(value string, modelWindow int64) int {
	if tokens, ok := ParseAutoCompactTokens(value); ok {
		return max(tokens, autoCompactMinTokens)
	}
	percent, ok := ParseAutoCompactPercent(value)
	if modelWindow <= 0 || !ok {
		return 0
	}
	tokens := int(modelWindow * int64(percent) / 100)
	return min(max(tokens, autoCompactMinTokens), autoCompactMaxTokens)
}

// IsZero reports whether the config carries no settings, so storage can persist
// SQL NULL and resolution can skip an empty config.
func (c AgentConfig) IsZero() bool {
	return c == AgentConfig{}
}

// Rank orders the modes by how much they let the agent do without asking:
// default < accept-edits < auto < bypass-permissions. Empty and unrecognized
// values rank as default, matching ports.NormalizePermissionMode. A new mode
// must get a case here; TestPermissionModeRankIsStrictlyOrdered lists them.
func (m PermissionMode) Rank() int {
	switch m {
	case PermissionModeAcceptEdits:
		return 1
	case PermissionModeAuto:
		return 2
	case PermissionModeBypassPermissions:
		return 3
	default:
		return 0
	}
}

// Valid reports whether the mode is one Tallpine knows. Empty counts as valid: it means
// "the adapter's own baseline", which is a legitimate choice rather than a missing
// one.
func (m PermissionMode) Valid() bool {
	switch m {
	case "", PermissionModeDefault, PermissionModeAcceptEdits,
		PermissionModeAuto, PermissionModeBypassPermissions:
		return true
	default:
		return false
	}
}

// Validate rejects values outside the typed vocabulary so a bad config is
// refused when it is set (CLI/API) rather than silently dropped at spawn.
func (c AgentConfig) Validate() error {
	switch {
	case c.Mode == "" || ValidEffort(c.Mode):
	case c.Mode == string(SessionModeTUI), c.Mode == string(SessionModeChat):
		return fmt.Errorf("invalid mode %q: agentConfig.mode is the agent's reasoning effort (%s); to choose %q or %q sessions use the project's defaultSessionMode or the spawn's --mode",
			c.Mode, effortList(), SessionModeTUI, SessionModeChat)
	default:
		return fmt.Errorf("invalid mode %q: want one of %s", c.Mode, effortList())
	}
	if c.Permissions.Valid() {
		return nil
	}
	return fmt.Errorf("invalid permissions %q: choose a supported permission mode", c.Permissions)
}

func effortList() string { return strings.Join(EffortOrder, ", ") }
