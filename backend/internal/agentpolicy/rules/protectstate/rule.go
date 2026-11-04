// Package protectstate is the opt-in rule that fences agents off Tallpine's own
// secrets and store. Agents run as the user, so without an OS sandbox any shell
// command can read the install token, the session key that mints scoped tokens,
// the database and the GitHub PAT. Turned on, Claude Code sessions run their
// shell in Claude's sandbox with reads of those files denied; the tallpine CLI
// keeps working over the daemon socket with the session's scoped token. Kiro
// sessions (terminal, chat, reviewer) run whole inside a Tallpine-generated
// macOS seatbelt profile (statefence) with the same paths denied and writes
// confined to the worktree, the session's scratch dirs and Kiro's own state.
// Codex terminal and chat sessions with no Codex sandbox (full access) run the
// same way; sandboxed Codex modes cannot nest it and keep the gap.
// DeepSeek Harness bypass sessions (dsh sandbox off) are fenced likewise;
// confined dsh modes and the read-only reviewer keep the gap.
//
// It is a file fence only, not data-loss prevention: network egress is not
// restricted, so whatever the agent may read can go to any destination. Claude Code,
// Kiro, full-access Codex and bypass DeepSeek Harness on macOS only in this version; other harnesses and systems launch unchanged.
// Protected sessions load the user's Claude settings only, never the repo's. Off by default; a user or a
// managed policy turns it on, and a project may turn it on but never off.
package protectstate

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Name is the rule's key in settings documents.
const Name = "protectState"

// FieldMode is the off/on field.
const FieldMode = "mode"

// Mode values, least strict first (tighten-only order).
const (
	Off = "off"
	On  = "on"
)

// FieldDevTooling is the strict/allow field for local dev tooling inside the fence.
const FieldDevTooling = "devTooling"

// DevTooling values, least strict first (tighten-only order).
const (
	DevAllow  = "allow"
	DevStrict = "strict"
)

// Credential-fence fields. Under the hardened profile the fence also hides the
// developer credential homes (see claudecode.CredentialHomes); these extend and
// relax that list. Entries are absolute paths or ~/-relative.
const (
	FieldCredentialPaths = "credentialPaths" //nolint:gosec // field name, not a credential
	FieldCredentialAllow = "credentialAllow" //nolint:gosec // field name, not a credential
)

// Rule implements agentpolicy.Rule.
type Rule struct{}

// Name implements agentpolicy.Rule.
func (Rule) Name() string { return Name }

// Schema implements agentpolicy.Rule.
func (Rule) Schema() agentpolicy.Schema {
	return agentpolicy.Schema{
		TitleKey: "settings.agentPolicy.title",
		Fields: []agentpolicy.Field{{
			Name:           FieldMode,
			Kind:           agentpolicy.KindEnum,
			Enum:           []string{Off, On},
			Default:        json.RawMessage(`"off"`),
			LabelKey:       "settings.agentPolicy.protectState.label",
			DescriptionKey: "settings.agentPolicy.protectState.description",
			OptionLabelKeys: map[string]string{
				Off: "settings.agentPolicy.protectState.off",
				On:  "settings.agentPolicy.protectState.on",
			},
			ProjectOverride: agentpolicy.ProjectTightenOnly,
			// A typo is reported and ignored rather than sandboxing every agent.
			OnInvalid: agentpolicy.InvalidIgnore,
		}, {
			// Widens the fence in exactly two ways: unix sockets may be bound under
			// the session's own temp dir (test servers, the merge gate, dev servers),
			// and the xcrun_db-* cache files in the per-user temp dir are writable
			// (git, clang and cgo shims). A project
			// may tighten it to strict but never loosen it.
			Name:           FieldDevTooling,
			Kind:           agentpolicy.KindEnum,
			Enum:           []string{DevAllow, DevStrict},
			Default:        json.RawMessage(`"allow"`),
			LabelKey:       "settings.agentPolicy.protectState.devTooling",
			DescriptionKey: "settings.agentPolicy.protectState.devToolingDescription",
			OptionLabelKeys: map[string]string{
				DevAllow:  "settings.agentPolicy.protectState.devToolingAllow",
				DevStrict: "settings.agentPolicy.protectState.devToolingStrict",
			},
			ProjectOverride: agentpolicy.ProjectTightenOnly,
			OnInvalid:       agentpolicy.InvalidStrictest,
		}, {
			Name:            FieldCredentialPaths,
			Kind:            agentpolicy.KindStringList,
			Default:         json.RawMessage(`[]`),
			LabelKey:        "settings.agentPolicy.protectState.credentialPaths",
			DescriptionKey:  "settings.agentPolicy.protectState.credentialPathsDescription",
			ProjectOverride: agentpolicy.ProjectAny, // hiding more only tightens
		}, {
			Name:           FieldCredentialAllow,
			Kind:           agentpolicy.KindStringList,
			Default:        json.RawMessage(`[]`),
			LabelKey:       "settings.agentPolicy.protectState.credentialAllow",
			DescriptionKey: "settings.agentPolicy.protectState.credentialAllowDescription",
			// Allowing a credential home loosens the fence: user or managed only.
			ProjectOverride: agentpolicy.ProjectNever,
		}},
	}
}

// Validate implements agentpolicy.Rule.
func (Rule) Validate(agentpolicy.Settings) error { return nil }

// Decide implements agentpolicy.Rule.
func (Rule) Decide(in agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	required := in.Profile == agentpolicy.ProfileHardened
	return Outcome{
		on: required || (s.String(FieldMode) == On && (!in.DevMode || in.Session.Kind != domain.KindOrchestrator)), required: required,
		devTooling: s.String(FieldDevTooling) != DevStrict,
		extra:      s.Strings(FieldCredentialPaths), allow: s.Strings(FieldCredentialAllow),
	}
}

// Outcome is whether the session's agent runs fenced off Tallpine state. The
// zero Outcome is off.
type Outcome struct {
	on       bool
	required bool
	// devTooling: the fence lets the shell bind unix sockets under the session's temp dir.
	devTooling bool
	extra      []string
	allow      []string
}

// DevTooling reports whether the fence also allows local dev tooling (unix
// sockets under the session's own temp dir, for test servers and the merge gate).
func (o Outcome) DevTooling() bool { return o.on && o.devTooling }

// HidesCredentials reports whether the fence also hides developer credential
// homes: only under the hardened profile, where the fence is required.
func (o Outcome) HidesCredentials() bool { return o.on && o.required }

// CredentialPaths are the extra paths to hide.
func (o Outcome) CredentialPaths() []string { return o.extra }

// CredentialAllow are the paths exempted from hiding (a default home or an
// extra path).
func (o Outcome) CredentialAllow() []string { return o.allow }

// Record implements agentpolicy.Outcome; the setting and its source are already
// in the decision record.
func (Outcome) Record() map[string]any { return nil }

// Enabled reports whether the agent must be fenced off Tallpine state.
func (o Outcome) Enabled() bool { return o.on }

// Required reports whether a session that cannot be fenced must not launch at
// all (the managed hardened profile). Seams refuse such launches with
// POLICY_REQUIRES_FENCE instead of starting them unfenced.
func (o Outcome) Required() bool { return o.required }

// Of returns the outcome of a decision; the zero Decision is off.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}

// Admit refuses a launch the outcome requires to be fenced when the adapter
// cannot fence it (fences false) on goos. It admits everything when the fence is
// not required, so callers run it unconditionally at every launch seam.
func (o Outcome) Admit(fences bool, harness, goos string) error {
	if !o.required || fences {
		return nil
	}
	if harness == "" {
		harness = "an unknown agent"
	}
	return &agentpolicy.FenceRequiredError{Harness: harness, GOOS: goos}
}

// Gap is why a session that must be fenced runs unfenced: Security mode is on
// (so the fence is wanted) but the adapter cannot fence harness on goos. Empty
// when the session is fenced, the fence is off, or the launch is refused
// instead (Required, see Admit). Shown on the session; never a refusal.
func (o Outcome) Gap(fences bool, harness, goos string) string {
	if !o.on || o.required || fences {
		return ""
	}
	if harness == "" {
		harness = "this agent"
	}
	return harness + " on " + goos + " cannot be fenced off Tallpine state"
}
