package domain

import (
	"encoding/json"
	"slices"
	"strings"
)

// Session roles a skill switch addresses. RoleReviewer (native reviews) is
// shared with the per-role prompt cache setting.
const (
	SkillRoleWorker       = "worker"
	SkillRoleOrchestrator = "orchestrator"
)

// SkillRoleNames lists the roles of skillRoles in display order.
var SkillRoleNames = []string{SkillRoleWorker, SkillRoleOrchestrator, RoleReviewer}

// SkillMasterKey is the SkillRoles entry that switches a skill off everywhere
// (false) regardless of role and agent; absent or true leaves it to the roles.
const SkillMasterKey = "all"

// skillAgentKeys are the SkillRoles entries of the agent types: a skill
// switched off there is not delivered to that agent in any role. Absent means
// on. Keys are camelCase because they are settings path segments.
var skillAgentKeys = map[AgentHarness]string{
	HarnessClaudeCode: "agentClaudeCode",
	HarnessCodex:      "agentCodex",
	HarnessKiro:       "agentKiro",
	HarnessDeepSeek:   "agentDeepseek",
}

// SkillAgentKey is the SkillRoles entry for harness, "" for an agent with no
// switch (a test fake).
func SkillAgentKey(harness string) string { return skillAgentKeys[AgentHarness(harness)] }

// SkillRoles maps a role to skill name -> on/off. A skill absent for a role
// follows the next layer (project, then app, then SkillDefault). Besides the
// roles it holds the master switch (SkillMasterKey) and per-agent switches
// (SkillAgentKey), which cut a skill off for every role.
type SkillRoles map[string]map[string]bool

// SkillAllowed reports whether the master switch and the agent-type switch
// leave skill id on for harness (project layer first, then app; on when
// unset). An empty harness checks the master switch only.
func SkillAllowed(project, app SkillRoles, harness, id string) bool {
	if on, set := ResolveSkillRole(project, app, SkillMasterKey, id); set && !on {
		return false
	}
	key := SkillAgentKey(harness)
	if key == "" {
		return true
	}
	on, set := ResolveSkillRole(project, app, key, id)
	return !set || on
}

// SkillSource is where a skill comes from, which decides its default and how
// a harness can honour a switch.
type SkillSource string

const (
	// SkillSourceRepo is a skill committed to the project repository.
	SkillSourceRepo SkillSource = "repo"
	// SkillSourceUser is a skill in the account's home directories.
	SkillSourceUser SkillSource = "user"
	// SkillSourceTallpine is a skill Tallpine installs; sessions always get it.
	SkillSourceTallpine SkillSource = "tallpine"
	// SkillSourcePlugin is a Claude Code plugin (switched by claudePlugins).
	SkillSourcePlugin SkillSource = "plugin"
	// SkillSourceCustom is an owner-managed skill under the Tallpine data dir.
	// Tallpine lists the enabled ones in the session's prompt.
	SkillSourceCustom SkillSource = "custom"
	// SkillSourceReviewerRepo is a skill under .tallpine/reviewer-skills on the
	// project's default branch. Only the reviewer gets it, as a text-only copy.
	SkillSourceReviewerRepo SkillSource = "reviewer-repo"
)

// SkillEffect says what a role's switch does on one harness.
type SkillEffect string

const (
	// SkillEnforced means the launch hides an off skill from the harness.
	SkillEnforced SkillEffect = "enforced"
	// SkillAdvisory means the harness loads the skill anyway; Tallpine only tells
	// the agent in its prompt not to use it.
	SkillAdvisory SkillEffect = "advisory"
	// SkillUnsupported means the switch changes nothing on this harness and role.
	SkillUnsupported SkillEffect = "unsupported"
)

// ResolveSkillRole returns a role's switch for skill id: the project value,
// else the app value. set is false when neither layer has one, and the caller
// uses SkillDefault.
func ResolveSkillRole(project, app SkillRoles, role, id string) (on, set bool) {
	if v, ok := project[role][id]; ok {
		return v, true
	}
	if v, ok := app[role][id]; ok {
		return v, true
	}
	return false, false
}

// SkillDefault is a role's switch for a skill nobody set: workers and
// orchestrators keep what their harness loads; reviewers get only what is
// switched on for them (B740 q1). The base-branch reviewer-skills directory
// exists only for the reviewer and reached the default branch through
// review, so it is on for the reviewer and never reaches another role (n3).
func SkillDefault(role string, src SkillSource) bool {
	if src == SkillSourceReviewerRepo {
		return role == RoleReviewer
	}
	return role != RoleReviewer
}

// SkillPromptDelivered reports whether Tallpine, not the harness, delivers
// skills from src: the session's prompt lists each enabled one by path, so a
// switch is enforced on every harness and the launch never names them.
func SkillPromptDelivered(src SkillSource) bool {
	return src == SkillSourceCustom || src == SkillSourceReviewerRepo
}

// SkillOn resolves a role's switch for skill id from source src on harness,
// after the master and agent-type switches (SkillAllowed).
func SkillOn(project, app SkillRoles, role, harness, id string, src SkillSource) bool {
	if !SkillAllowed(project, app, harness, id) {
		return false
	}
	if on, set := ResolveSkillRole(project, app, role, id); set {
		return on
	}
	return SkillDefault(role, src)
}

// SkillEffectFor is what a switch does for a skill from src in a role's
// launch on harness. It mirrors the launch code:
//   - Tallpine's own skills are a fixed prompt pointer: never switchable.
//   - Custom and reviewer-repo skills are listed in the prompt only when on:
//     enforced on every harness (reviewer-repo for the reviewer only).
//   - Plugins: Claude Code enabledPlugins; no other harness loads them.
//   - Reviewers never load the reviewed checkout's agent config, so a repo
//     skill cannot reach them either way.
//   - Claude Code: --settings skillOverrides {name: "off"}.
//   - Codex: -c skills.config=[{name, enabled=false}].
//   - Kiro: the generated agent's skill:// resources list only enabled skills.
//   - DeepSeek loads repo .agents/skills with no switch (advisory); its
//     reviewer runs in an empty directory and loads none.
func SkillEffectFor(harness string, src SkillSource, role string) SkillEffect {
	switch {
	case src == SkillSourceTallpine:
		return SkillUnsupported
	case src == SkillSourceReviewerRepo:
		if role == RoleReviewer {
			return SkillEnforced
		}
		return SkillUnsupported
	case src == SkillSourceCustom:
		return SkillEnforced
	case src == SkillSourcePlugin:
		if harness == string(HarnessClaudeCode) {
			return SkillEnforced
		}
		return SkillUnsupported
	case role == RoleReviewer && src == SkillSourceRepo:
		return SkillUnsupported
	}
	switch AgentHarness(harness) {
	case HarnessClaudeCode, HarnessCodex, HarnessKiro:
		return SkillEnforced
	case HarnessDeepSeek:
		if role == RoleReviewer {
			return SkillUnsupported
		}
		return SkillAdvisory
	}
	return SkillAdvisory
}

// SkillsOffFor lists the skills explicitly switched off for a role on harness
// (by its role, master or agent-type switch), sorted: the names the advisory
// prompt section names.
func SkillsOffFor(project, app SkillRoles, role, harness string) []string {
	var out []string
	for _, layer := range []SkillRoles{project, app} {
		for _, key := range []string{role, SkillMasterKey, SkillAgentKey(harness)} {
			if key == "" {
				continue
			}
			for id := range layer[key] {
				roleOn, set := ResolveSkillRole(project, app, role, id)
				if ((set && !roleOn) || !SkillAllowed(project, app, harness, id)) && !slices.Contains(out, id) {
					out = append(out, id)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// SkillLaunch is what a role's switches give one launch. Off are skill names
// the harness must hide (Claude skillOverrides, Codex skills.config); Resources
// are the Kiro skill:// resources of the enabled skills.
type SkillLaunch struct {
	Off       []string `json:"off,omitempty"`
	Resources []string `json:"resources,omitempty"`
}

// Encode returns l as the AgentLaunchOptions.Skills string ("" when empty).
func (l SkillLaunch) Encode() string {
	if len(l.Off) == 0 && len(l.Resources) == 0 {
		return ""
	}
	b, _ := json.Marshal(l)
	return string(b)
}

// SkillLaunch decodes the launch's skill switches; malformed or empty yields none.
func (o AgentLaunchOptions) SkillLaunch() SkillLaunch {
	var l SkillLaunch
	if s := strings.TrimSpace(o.Skills); s != "" {
		_ = json.Unmarshal([]byte(s), &l)
	}
	return l
}
