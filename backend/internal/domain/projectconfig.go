package domain

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// ProjectConfig is the typed per-project configuration — the SQLite twin of the
// legacy tallpine.yaml `projects.<id>` block. It is persisted as one
// JSON blob per project and resolved at spawn. Each field is typed and
// validated; there is no free-form map.
//
// Only fields with a live consumer are modeled: DefaultBranch, Env, Symlinks,
// PostCreate, AgentConfig, prompt rules, and the role overrides are consumed at
// spawn; SessionPrefix feeds the display prefix. Settings whose consumers do not
// yet exist (tracker/SCM per-project config) are intentionally absent and land in
// focused follow-up PRs alongside the code that reads them.
type ProjectConfig struct {
	// CanonicalRepoURL explicitly trusts one upstream repository for PR claims.
	// Numeric PR references use this repository when set; checkout/push stays on origin.
	CanonicalRepoURL string `json:"canonicalRepoURL,omitempty"`
	// GitHubUser pins the signed-in `gh` account used for this project's sessions
	// and PR observation. Empty lets Tallpine pick the account that can push to the repo.
	GitHubUser string `json:"githubUser,omitempty"`
	// DefaultBranch is the base branch new session worktrees are created from.
	// Empty and DefaultBranchAuto both mean infer each repository's Git default.
	DefaultBranch string `json:"defaultBranch,omitempty"`
	// SessionPrefix overrides the displayed session-id prefix.
	SessionPrefix string `json:"sessionPrefix,omitempty"`

	// Env are extra environment variables forwarded into worker session
	// runtimes. The VO_ namespace is reserved for Tallpine-owned variables.
	Env map[string]string `json:"env,omitempty"`
	// Symlinks are repo-relative paths symlinked into each session workspace.
	Symlinks []string `json:"symlinks,omitempty"`
	// PostCreate are shell commands run in the workspace after it is created,
	// each for at most its own time limit (ParseTimedScriptCommand) or
	// DefaultScriptStepTimeoutMinutes.
	PostCreate []string `json:"postCreate,omitempty"`
	// CloneWorktreeInclude opts the project into seeding directories matched by
	// .worktreeinclude (for example node_modules/) with a copy-on-write clone
	// instead of a file-by-file copy. Effective on macOS/APFS only; any other
	// platform, volume or unsafe tree falls back to the normal copy. Pair it
	// with a PostCreate reconciliation such as `npm install --prefer-offline`.
	CloneWorktreeInclude bool `json:"cloneWorktreeInclude,omitempty"`
	// PreRemove are shell commands run before a Tallpine-managed Git worktree is
	// permanently removed, each within its own time limit when it sets one.
	PreRemove []string `json:"preRemove,omitempty"`

	// AgentRules are project-specific standing instructions for worker sessions.
	// At most MaxAgentRulesBytes; the combined rules are capped again at spawn.
	AgentRules string `json:"agentRules,omitempty"`
	// AgentRulesFile is a repo-relative Markdown/text file whose contents are
	// appended to AgentRules for worker sessions.
	AgentRulesFile string `json:"agentRulesFile,omitempty"`
	// VerifyCommand is a single-line command worker prompts tell the worker to
	// run, and pass, before pushing. It is prompt-only: the daemon never
	// executes it.
	VerifyCommand string `json:"verifyCommand,omitempty"`
	// OrchestratorRules are project-specific standing instructions for
	// orchestrator sessions.
	OrchestratorRules string `json:"orchestratorRules,omitempty"`

	// DefaultSessionMode is the interface ("tui" or "chat") new worker sessions
	// get when the spawn does not request one. Empty defers to the daemon-wide
	// default. It is not AgentConfig.Mode, which is the reasoning effort.
	DefaultSessionMode SessionMode `json:"defaultSessionMode,omitempty" enum:"chat,tui"`

	// AgentConfig is the default agent config for the project.
	AgentConfig AgentConfig `json:"agentConfig,omitempty"`
	// Worker and Orchestrator are role-specific harness/agent-config overrides.
	Worker       RoleOverride `json:"worker,omitempty"`
	Orchestrator RoleOverride `json:"orchestrator,omitempty"`
	// Planner overrides the harness and model of the project's planners (B205).
	// Empty inherits the orchestrator's, else the worker's; only the harness and
	// agentConfig fields apply. A managed policy (policy.planner) wins over it.
	Planner RoleOverride `json:"planner,omitempty"`
	// PlanningMode says when the project plans before it builds: auto (the
	// orchestrator decides; also the empty value), always, or off.
	PlanningMode PlanningMode `json:"planningMode,omitempty"`

	// Reviewers names the agent(s) that review a worker's PR when a review is
	// triggered. It is configured independently of the Worker override; an empty
	// list falls back to claude-code (see ResolveReviewerHarness).
	Reviewers []ReviewerConfig `json:"reviewers,omitempty"`
	// ReviewRisk picks the reviewer model from the PR's changed paths when a
	// review is triggered without --model.
	ReviewRisk ReviewRiskConfig `json:"reviewRisk,omitempty"`
	// TrackerIntake controls issue-driven worker spawning. It is opt-in and
	// read-only toward the tracker in v1: matching issues spawn sessions, but the
	// tracker is not commented on or transitioned.
	TrackerIntake TrackerIntakeConfig `json:"trackerIntake,omitempty"`

	// ContainerReap controls whether Tallpine reaps a worker session's tallpine.session-
	// labeled Docker containers on terminal state / kill. Enabled by default;
	// set Disabled to opt a project out entirely. Per-container sparing uses
	// the tallpine.spare=true label instead (see dockerreap.SpareLabel) so the
	// opt-out travels with the container at `docker run` time rather than
	// drifting out of sync with a project-config list.
	ContainerReap ContainerReapConfig `json:"containerReap,omitempty"`

	// AutoReview controls whether new worker sessions spawned for this project
	// have automatic PR review enabled by default. The default (false) leaves
	// sessions with auto-review off; enabling it copies the setting into each
	// new session at spawn time. Users can still override the per-session toggle
	// after spawn. Nil is off.
	AutoReview *bool `json:"autoReview,omitempty"`

	// AutoReviewOnReport starts the native review as soon as a worker with
	// auto-review enabled reports a PR ready, instead of waiting for the idle
	// sweep. Nil (the default) means on; false opts out.
	AutoReviewOnReport *bool `json:"autoReviewOnReport,omitempty"`

	// AutoReviewMaxRounds, when above zero, stops automatic re-reviews of a PR
	// after this many reviews that requested changes, and tells the orchestrator.
	// Zero (the default) never stops.
	AutoReviewMaxRounds int `json:"autoReviewMaxRounds,omitempty"`
	// ApproveIdenticalPatch keeps a review approval when the PR head changes but
	// its patch is identical (a clean rebase): no new review is started. Off by
	// default.
	ApproveIdenticalPatch bool `json:"approveIdenticalPatch,omitempty"`

	// TerminateOnPRMerge is the default for each new worker's own
	// terminate-on-merge flag: when set, a worker ends once its PRs are merged
	// and none is open. Copied at spawn like AutoReview; nil is on (B407).
	TerminateOnPRMerge *bool `json:"terminateOnPrMerge,omitempty"`

	// ReportDigestCount, when above zero, flushes the orchestrator's waiting
	// quiet (non-decision) worker reports as one batch as soon as this many are
	// queued, instead of waiting out the quiet delay. Zero (the default) keeps
	// the time-based delay only.
	ReportDigestCount int `json:"reportDigestCount,omitempty"`

	// SkillsOfferOff turns off the automatic notice that offers to make the
	// project's instruction file and skills visible to its agents (on project
	// add and on session start). Settings > Skills turns it back on.
	SkillsOfferOff bool `json:"skillsOfferOff,omitempty"`

	// SkillsDeselected names repo skills the user chose not to bridge to other
	// harnesses (Settings > Skills). They are no longer offered or created.
	SkillsDeselected []string `json:"skillsDeselected,omitempty"`

	// NoCI declares that the project's repository has no CI, so Tallpine review
	// approval is the merge gate. When set and the SCM provider reports no
	// checks at all for a PR head, CI counts as passing for merge readiness
	// (ready-to-merge state, notifications and `tallpine pr merge`) only once a Tallpine
	// review approved that exact head; a provider approval does not count (it
	// is not tied to a commit), and an unreviewed PR stays blocked.
	// Reported pending or failing checks still block. Off by default: in a
	// repository with CI a fresh push can briefly have no registered checks.
	NoCI bool `json:"noCI,omitempty"`

	// PlanAutoApprove approves a submitted plan at once, without the owner's
	// word, for unattended throwaway projects (benchmarks, test runs). nil
	// follows the app default (off); an explicit false stays off. Only a human
	// may set it, and a managed policy.planner.autoApprove of "off" locks it off.
	PlanAutoApprove *bool `json:"planAutoApprove,omitempty"`

	// PlanInPlace lets an item that needs a plan be planned by the implementer
	// itself (B335): `tallpine spawn --backlog` starts an implementer that
	// authors the plan, waits read-only for approval and then builds, instead of
	// refusing with PLAN_REQUIRED. Off by default; a managed
	// policy.planner.inPlace of "off" locks it off.
	PlanInPlace bool `json:"planInPlace,omitempty"`

	// Eval marks a throwaway evaluation project (B335). The orchestrator that
	// registered it may then drive its backlog, plans and sessions from its own
	// project and answer and approve plans as the owner's proxy; a project that
	// is not marked keeps the owner-message approval guard. Only the user or the
	// creating orchestrator may set it, and the scope gate opens routes only for
	// a project that is both created by the caller and marked.
	Eval bool `json:"eval,omitempty"`

	// Models is the project's model policy: the Codex default model and the
	// denylist/allowlist checked at spawn. Unset fields use the shipped defaults.
	Models ModelPolicy `json:"models,omitempty"`
	// PromptCacheTTL maps a session role ("orchestrator", "worker", "reviewer")
	// to the CLAUDE_CODE_PROMPT_CACHE_TTL value claude-code sessions of that
	// role launch with. A role absent from the map uses the shipped default
	// (reviewer 5m, others unset); an explicit empty value leaves the variable
	// unset for that role.
	PromptCacheTTL map[string]string `json:"promptCacheTTL,omitempty"`
	// ClaudePlugins maps a session role ("orchestrator", "worker", "reviewer")
	// to the Claude Code enabledPlugins override ("name@marketplace" -> on/off)
	// that role's sessions launch with. A role absent from the map loads the
	// account's plugins unchanged.
	ClaudePlugins map[string]map[string]bool `json:"claudePlugins,omitempty"`
	// ClaudeAutoMemory maps a session role to whether Claude Code auto-memory
	// is on for it. A role absent from the map inherits the account setting;
	// false starts that role's sessions with autoMemoryEnabled off.
	ClaudeAutoMemory map[string]bool `json:"claudeAutoMemory,omitempty"`

	// ChatHibernation is the project-level opt-out for idle Chat hibernation.
	// A pointer so an unset opt-out is omitted from the stored config.
	ChatHibernation *ChatHibernationConfig `json:"chatHibernation,omitempty"`
}

// ChatHibernationDisabled reports whether the project opted out of idle Chat
// hibernation.
func (c ProjectConfig) ChatHibernationDisabled() bool {
	return c.ChatHibernation != nil && c.ChatHibernation.Disabled
}

// ChatHibernationConfig is the project-level opt-out for idle Chat
// hibernation. The app-wide setting still applies when this is unset.
type ChatHibernationConfig struct {
	// Disabled keeps every Chat session of this project awake however long
	// it is idle.
	Disabled bool `json:"disabled,omitempty"`
}

// ContainerReapConfig is the project-level opt-out for #2652's Docker
// container reaping on session terminal state.
type ContainerReapConfig struct {
	// Disabled turns off container reaping for every session in this project.
	// Per-container sparing (tallpine.spare=true) is unaffected either way.
	Disabled bool `json:"disabled,omitempty"`
}

// RoleOverride overrides the harness and/or agent config for a session role.
type RoleOverride struct {
	Harness     AgentHarness `json:"agent,omitempty"`
	AgentConfig AgentConfig  `json:"agentConfig,omitempty"`
	// AutoCompactWindow is the compaction window the role's sessions start with
	// when they choose none of their own: a compaction choice
	// (ValidAutoCompactWindow) or AutoCompactOff. Only the orchestrator role
	// accepts it; see DefaultAutoCompactChoice.
	AutoCompactWindow string `json:"autoCompactWindow,omitempty"`
	// IdleCompactAt is the context size above which the role's idle sessions
	// compact between tasks (B204): ValidIdleCompactAt. Only the orchestrator
	// role accepts it; empty means OrchestratorIdleCompactDefault.
	IdleCompactAt string `json:"idleCompactAt,omitempty"`
	// IdleAction is what an idle orchestrator past its threshold does (B374):
	// IdleActionCompact (empty, the default) or IdleActionHandoff. Only the
	// orchestrator role accepts it.
	IdleAction string `json:"idleAction,omitempty"`
	// ResearchCompactWindow is the compaction window of an orchestrator in
	// research mode (B413): ValidResearchCompactWindow. AutoCompactOff disables
	// research mode for the project; empty means
	// OrchestratorResearchCompactDefault. Only the orchestrator role accepts it.
	ResearchCompactWindow string `json:"researchCompactWindow,omitempty"`
}

const (
	// DefaultBranchAuto tells callers to infer the Git default branch for each
	// repository instead of naming one branch for the whole project.
	DefaultBranchAuto = "auto"
	// DefaultBranchName is the branch Tallpine selects when it creates a repository.
	// Automatic resolution never uses it as a guess for existing repositories.
	DefaultBranchName = "main"
)

// DefaultProjectConfig returns the config a project has when it sets nothing:
// automatic per-repository branch resolution. Every other field defaults to
// its zero value (no env/symlinks/post-create, agent + role defaults).
func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		DefaultBranch: DefaultBranchAuto,
	}
}

// WithDefaults overlays DefaultProjectConfig onto c, filling only fields the
// project left unset. A set field is always preserved.
func (c ProjectConfig) WithDefaults() ProjectConfig {
	def := DefaultProjectConfig()
	if c.DefaultBranch == "" {
		c.DefaultBranch = def.DefaultBranch
	}
	c.TrackerIntake = c.TrackerIntake.WithDefaults()
	return c
}

// WorktreeBaseBranch translates project configuration into the workspace
// interface. An empty value tells the workspace adapter to resolve a remote
// HEAD independently for the repository it is materializing.
func (c ProjectConfig) WorktreeBaseBranch() string {
	branch := c.WithDefaults().DefaultBranch
	if branch == DefaultBranchAuto {
		return ""
	}
	return branch
}

// IsZero reports whether the config carries no settings, so storage can persist
// SQL NULL and resolution can skip an empty config.
func (c ProjectConfig) IsZero() bool {
	return reflect.DeepEqual(c, ProjectConfig{})
}

// dropAgentConfigTuning clears the harness-specific launch choices (model,
// effort, mode) that were tuned for a harness being replaced. Permissions are
// harness-neutral and stay.
func dropAgentConfigTuning(c AgentConfig) AgentConfig {
	c.Model, c.Effort, c.Mode = "", "", ""
	return c
}

// NormalizeHarnesses returns a copy of c in which every worker, orchestrator
// and reviewer harness that earlier builds shipped and this one removed is
// replaced by its fallback (an id that never existed is left for Validate to
// reject), together with the model/effort/mode that were chosen for it. The
// receiver is not modified.
func (c ProjectConfig) NormalizeHarnesses() ProjectConfig {
	for _, ro := range []*RoleOverride{&c.Worker, &c.Orchestrator, &c.Planner} {
		if ro.Harness.IsRemoved() {
			ro.Harness = FallbackHarness
			ro.AgentConfig = dropAgentConfigTuning(ro.AgentConfig)
		}
	}
	if len(c.Reviewers) > 0 {
		reviewers := make([]ReviewerConfig, len(c.Reviewers))
		copy(reviewers, c.Reviewers)
		for i := range reviewers {
			if reviewers[i].Harness.IsRemoved() {
				reviewers[i].Harness = FallbackReviewerHarness
				reviewers[i].AgentConfig = dropAgentConfigTuning(reviewers[i].AgentConfig)
			}
		}
		c.Reviewers = reviewers
	}
	return c
}

// IsGitHubLogin reports whether s is a valid GitHub username.
func IsGitHubLogin(s string) bool { return githubLoginPattern.MatchString(s) }

var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// MaxAgentRulesBytes caps the worker standing instructions (inline agentRules,
// and separately the combined text sent to a worker) so a huge rules file
// cannot inflate every worker's system prompt.
const MaxAgentRulesBytes = 8 * 1024

// ValidateAgentRulesCap rejects agentRules over MaxAgentRulesBytes unless they
// are no longer than the stored value (previous). A project saved before the
// cap keeps its legacy rules and can still change unrelated settings; spawn
// truncates them. Only newly set or grown rules are refused.
func (c ProjectConfig) ValidateAgentRulesCap(previous string) error {
	if len(c.AgentRules) <= MaxAgentRulesBytes || len(c.AgentRules) <= len(previous) {
		return nil
	}
	return fmt.Errorf("agentRules is %d bytes, over the %d byte limit; move the detail into a file and set agentRulesFile (the combined rules sent to a worker are also capped at %d bytes)", len(c.AgentRules), MaxAgentRulesBytes, MaxAgentRulesBytes)
}

// Validate rejects values outside the typed vocabulary so a bad config is
// refused when it is set (CLI/API) rather than surfacing at spawn. A harness
// that was removed from this build (see IsRemoved) is not rejected: stored configuration may
// still name one and a client resubmitting it must not be locked out, so it is
// validated as its fallback (see NormalizeHarnesses).
func (c ProjectConfig) Validate() error {
	c = c.NormalizeHarnesses()
	if c.CanonicalRepoURL != "" {
		if err := c.ValidateCanonicalRepository(c.CanonicalRepoURL); err != nil {
			return err
		}
	}
	if c.GitHubUser != "" && !IsGitHubLogin(c.GitHubUser) {
		return fmt.Errorf("githubUser %q is not a valid GitHub login", c.GitHubUser)
	}
	if _, err := ParseSessionMode(string(c.DefaultSessionMode)); err != nil {
		return fmt.Errorf("defaultSessionMode: %w", err)
	}
	if err := c.AgentConfig.Validate(); err != nil {
		return err
	}
	if err := validateNameComponent("sessionPrefix", c.SessionPrefix); err != nil {
		return err
	}
	seenEnv := make(map[string]bool, len(c.Env))
	for key, value := range c.Env {
		if !validEnvName(key) || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("env %q: invalid variable name or value", key)
		}
		folded := strings.ToUpper(key)
		if strings.HasPrefix(folded, brand.EnvPrefix) {
			return fmt.Errorf("env %q: the %s prefix is reserved", key, brand.EnvPrefix)
		}
		if seenEnv[folded] {
			return fmt.Errorf("env %q: duplicate variable name", key)
		}
		seenEnv[folded] = true
	}
	if !c.PlanningMode.Valid() {
		return fmt.Errorf("planningMode: %q is not auto, always or off", c.PlanningMode)
	}
	for role, ro := range map[string]RoleOverride{"worker": c.Worker, "orchestrator": c.Orchestrator, "planner": c.Planner} {
		if ro.Harness != "" && !ro.Harness.IsKnown() {
			return fmt.Errorf("%s.agent: unknown harness %q", role, ro.Harness)
		}
		if err := ro.AgentConfig.Validate(); err != nil {
			return fmt.Errorf("%s.%w", role, err)
		}
	}
	if err := c.validateIdle(); err != nil {
		return err
	}
	if err := c.validateResearch(); err != nil {
		return err
	}
	if c.Worker.AutoCompactWindow != "" {
		return fmt.Errorf("worker.autoCompactWindow: only the orchestrator role sets a compaction window")
	}
	if w := c.Orchestrator.AutoCompactWindow; w != AutoCompactOff && !ValidAutoCompactWindow(w) {
		return fmt.Errorf("orchestrator.autoCompactWindow: %q is not a size from 50k to 1m, a percent from %d%% to %d%%, or %s", w, AutoCompactPercentMin, AutoCompactPercentMax, AutoCompactOff)
	}
	for _, s := range c.Symlinks {
		if err := validateRepoRelative(s); err != nil {
			return fmt.Errorf("symlink %q: %w", s, err)
		}
	}
	if err := validateScriptSteps("postCreate", c.PostCreate); err != nil {
		return err
	}
	if err := validateScriptSteps("preRemove", c.PreRemove); err != nil {
		return err
	}
	if err := validateRepoRelative(c.AgentRulesFile); err != nil {
		return fmt.Errorf("agentRulesFile %q: %w", c.AgentRulesFile, err)
	}
	if err := validateVerifyCommand(c.VerifyCommand); err != nil {
		return err
	}
	for i, rv := range c.Reviewers {
		if !rv.Harness.IsKnown() {
			return fmt.Errorf("reviewers[%d].harness: unknown harness %q", i, rv.Harness)
		}
		if err := rv.AgentConfig.Validate(); err != nil {
			return fmt.Errorf("reviewers[%d].agentConfig: %w", i, err)
		}
	}
	if err := c.TrackerIntake.Validate(); err != nil {
		return err
	}
	return nil
}

// MaxVerifyCommandLen bounds VerifyCommand so it stays a one-line prompt hint.
const MaxVerifyCommandLen = 500

func validateVerifyCommand(cmd string) error {
	if len(cmd) > MaxVerifyCommandLen {
		return fmt.Errorf("verifyCommand: must be at most %d characters", MaxVerifyCommandLen)
	}
	for _, r := range cmd {
		if unicode.IsControl(r) {
			return fmt.Errorf("verifyCommand: must be a single line without control characters")
		}
	}
	return nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func validateNoWhitespaceField(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s: must not have leading or trailing whitespace", name)
	}
	return nil
}

func validateNameComponent(name, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if strings.ContainsAny(trimmed, `/\`) || trimmed == "." || trimmed == ".." {
		return fmt.Errorf("%s: must not contain path separators or traversal components", name)
	}
	return nil
}

// validateRepoRelative refuses paths that would let a project config escape
// its repo root: absolute paths and any ".." segment (before or after Clean).
// The same guard runs at spawn time as defense-in-depth, but enforcing it here
// rejects bad config when it is set rather than at every later spawn.
func validateRepoRelative(p string) error {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" {
		return nil
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return fmt.Errorf("path must be repo-relative and must not escape the project root")
	}
	clean := filepath.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path must be repo-relative and must not escape the project root")
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return fmt.Errorf("path must be repo-relative and must not escape the project root")
		}
	}
	return nil
}
