package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// agentConfig mirrors the daemon's typed domain.AgentConfig for the CLI client.
type agentConfig struct {
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Permissions string `json:"permissions,omitempty"`
}

// roleOverride mirrors domain.RoleOverride.
type roleOverride struct {
	Agent       string      `json:"agent,omitempty"`
	AgentConfig agentConfig `json:"agentConfig,omitempty"`
}

// trackerIntakeConfig mirrors domain.TrackerIntakeConfig.
type trackerIntakeConfig struct {
	Enabled  bool   `json:"enabled,omitempty"`
	Provider string `json:"provider,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Assignee string `json:"assignee,omitempty"`
}

// reviewerConfig mirrors domain.ReviewerConfig.
type reviewerConfig struct {
	Harness     string       `json:"harness"`
	AgentConfig *agentConfig `json:"agentConfig,omitempty"`
}

type containerReapConfig struct {
	Disabled bool `json:"disabled,omitempty"`
}

type chatHibernationConfig struct {
	Disabled bool `json:"disabled,omitempty"`
}

// projectConfig mirrors the daemon's typed domain.ProjectConfig for the CLI
// client. The CLI sets common fields via flags and the whole object via
// --config-json.
type projectConfig struct {
	ContainerReap     *containerReapConfig `json:"containerReap,omitempty"`
	CanonicalRepoURL  string               `json:"canonicalRepoURL,omitempty"`
	GitHubUser        string               `json:"githubUser,omitempty"`
	DefaultBranch     string               `json:"defaultBranch,omitempty"`
	SessionPrefix     string               `json:"sessionPrefix,omitempty"`
	Env               map[string]string    `json:"env,omitempty"`
	Symlinks          []string             `json:"symlinks,omitempty"`
	PostCreate        []string             `json:"postCreate,omitempty"`
	AgentRules        string               `json:"agentRules,omitempty"`
	AgentRulesFile    string               `json:"agentRulesFile,omitempty"`
	VerifyCommand     string               `json:"verifyCommand,omitempty"`
	OrchestratorRules string               `json:"orchestratorRules,omitempty"`
	AgentConfig       agentConfig          `json:"agentConfig,omitempty"`
	Worker            roleOverride         `json:"worker,omitempty"`
	Orchestrator      roleOverride         `json:"orchestrator,omitempty"`
	TrackerIntake     trackerIntakeConfig  `json:"trackerIntake,omitempty"`
	// AutoReview and TerminateOnPRMerge are tri-state: nil inherits the
	// app-wide default, so an explicit false must survive set-config.
	AutoReview         *bool                    `json:"autoReview,omitempty"`
	TerminateOnPRMerge *bool                    `json:"terminateOnPrMerge,omitempty"`
	ReviewRisk         *domain.ReviewRiskConfig `json:"reviewRisk,omitempty"`
	Reviewers          []reviewerConfig         `json:"reviewers,omitempty"`
	// CloneWorktreeInclude must round-trip through set-config so a CLI edit
	// of another field never silently turns it off.
	CloneWorktreeInclude bool `json:"cloneWorktreeInclude,omitempty"`
	// DefaultSessionMode round-trips for the same reason.
	DefaultSessionMode string `json:"defaultSessionMode,omitempty"`
	// ChatHibernation round-trips for the same reason as CloneWorktreeInclude.
	ChatHibernation *chatHibernationConfig `json:"chatHibernation,omitempty"`
	// NoCI round-trips for the same reason as CloneWorktreeInclude.
	NoCI bool `json:"noCI,omitempty"`
	// PlanAutoApprove round-trips for the same reason and is tri-state like
	// AutoReview: nil inherits the app default, an explicit false stays off.
	// Only a person can set it; the daemon refuses it from any agent session.
	PlanAutoApprove *bool `json:"planAutoApprove,omitempty"`
	// PlanInPlace round-trips for the same reason (B335).
	PlanInPlace bool `json:"planInPlace,omitempty"`
	// Eval round-trips for the same reason (B335).
	Eval bool `json:"eval,omitempty"`
	// ClaudePlugins and ClaudeAutoMemory round-trip for the same reason.
	ClaudePlugins    map[string]map[string]bool `json:"claudePlugins,omitempty"`
	ClaudeAutoMemory map[string]bool            `json:"claudeAutoMemory,omitempty"`
}

// setConfigRequest mirrors the daemon's SetConfigInput body for
// PUT /api/v1/projects/{id}/config.
type setConfigRequest struct {
	Config projectConfig `json:"config"`
}

type projectSetConfigOptions struct {
	canonicalRepoURL   string
	githubUser         string
	defaultBranch      string
	sessionPrefix      string
	model              string
	permission         string
	workerAgent        string
	orchestratorAgent  string
	workerEffort       string
	orchestratorEffort string
	reviewerEffort     string
	agentRules         string
	agentRulesFile     string
	orchestratorRules  string
	// orchestratorCompactWindow is orchestrator.autoCompactWindow.
	orchestratorCompactWindow string
	// orchestratorIdleCompact is orchestrator.idleCompactAt.
	orchestratorIdleCompact string
	// orchestratorResearchWindow is orchestrator.researchCompactWindow (B413).
	orchestratorResearchWindow string
	// orchestratorIdleAction is orchestrator.idleAction.
	orchestratorIdleAction string
	env                    []string
	unsetEnv               []string
	symlink                []string
	postCreate             []string
	trackerIntake          bool
	noCI                   bool
	planAutoApprove        bool
	planAutoApproveInherit bool
	planInPlace            bool
	trackerRepo            string
	trackerAssignee        string
	reviewers              []string
	configJSON             string
	clear                  bool
	json                   bool

	defaultSessionMode string
}

func newProjectSetConfigCommand(ctx *commandContext) *cobra.Command {
	var opts projectSetConfigOptions
	cmd := &cobra.Command{
		Use:   "set-config <id>",
		Short: "Set the per-project config",
		Long: "Update a project's per-project config (branch, session prefix, env, " +
			"symlinks, post-create, rules, agent model/permissions, role overrides, tracker intake, reviewers). The config " +
			"is resolved when a session spawns.\n\n" +
			"Field flags MERGE into the existing config: only the flags you pass change, and " +
			"every other field (including ones this CLI has no flag for) is kept. A scalar flag " +
			"given an empty value (e.g. --canonical-repo-url \"\") removes that field; --env merges by key; " +
			"--symlink, --post-create and --reviewer replace their whole list (pass one empty value to clear it); " +
			"--tracker-intake=false disables intake and --no-ci=false clears the no-CI merge gate.\n\n" +
			"The merge reads the config and writes it back in two requests with no revision check, so a " +
			"concurrent config change (another set-config or a settings save) made in between is overwritten.\n\n" +
			"Passing only --github-user pins (or, with an empty value, clears) the project's GitHub account through the settings route, which an orchestrator may use on its own project when the owner asks for it.\n\n" +
			"Passing only --agent-rules, --orchestrator-rules, --orchestrator-compact-window, --orchestrator-idle-compact and/or --orchestrator-research-window writes just those fields " +
			"through the rules endpoint; that is the only config change an orchestrator session may make, and only on its own project.\n\n" +
			"--config-json REPLACES the whole config with the given object (overriding field flags), " +
			"and --clear removes all config.",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := cobra.ExactArgs(1)(cmd, args); err != nil {
				return usageError{err}
			}
			if strings.TrimSpace(args[0]) == "" {
				return usageError{errors.New("usage: project id is required")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			var res projectResult
			if cmd.Flags().Changed("github-user") && opts.githubUser != "" {
				listed, err := requireSignedInGitHubUser(cmd.Context(), ctx, opts.githubUser, cmd.ErrOrStderr())
				if err != nil {
					return err
				}
				opts.githubUser = listed
			}
			if !opts.clear && opts.configJSON == "" && onlyGitHubUserFlag(cmd.Flags().Changed) {
				return setGitHubUserOnly(cmd.Context(), ctx, cmd, id, opts.githubUser, opts.json)
			}
			if !opts.clear && opts.configJSON == "" && onlyRulesFlags(cmd.Flags().Changed) {
				// The narrow rules endpoint keeps every other field server-side
				// and is the one config write an orchestrator token may make on
				// its own project.
				if err := ctx.putJSON(cmd.Context(), "projects/"+url.PathEscape(id)+"/rules", rulesRequest(cmd.Flags().Changed, opts), &res); err != nil {
					return err
				}
				return reportProjectConfig(cmd, opts, res)
			}
			var req any
			if opts.clear || opts.configJSON != "" {
				config, err := buildProjectConfig(opts)
				if err != nil {
					return err
				}
				req = map[string]any{"config": config}
			} else {
				merged, err := ctx.mergeProjectConfigFlags(cmd.Context(), id, cmd.Flags().Changed, opts)
				if err != nil {
					return err
				}
				req = map[string]any{"config": merged}
			}
			if err := ctx.putJSON(cmd.Context(), "projects/"+url.PathEscape(id)+"/config", req, &res); err != nil {
				return err
			}
			if err := reportProjectConfig(cmd, opts, res); err != nil {
				return err
			}
			if cmd.Flags().Changed("github-user") && !opts.json {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), githubAccountNotice)
				return err
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.defaultBranch, "default-branch", "", "Base branch for new worktrees; auto infers each repository's Git default")
	f.StringVar(&opts.githubUser, "github-user", "", "Signed-in gh account to use for this project; it must be signed in (see `tallpine github accounts`). Empty returns to auto-pick (the account that can push to the repo). Applies to sessions started afterwards")
	f.StringVar(&opts.canonicalRepoURL, "canonical-repo-url", "", "Explicit upstream HTTPS repository URL for PR claims; may be on another host when the repo moved, then it replaces origin for PR tracking")
	f.StringVar(&opts.sessionPrefix, "session-prefix", "", "Displayed session-id prefix")
	f.StringVar(&opts.defaultSessionMode, "default-session-mode", "", "Interface for new worker sessions: tui or chat (empty clears; not the reasoning effort in agentConfig.mode)")
	f.StringVar(&opts.model, "model", "", "Agent model override (e.g. claude-opus-4-5)")
	f.StringVar(&opts.permission, "permission", "", "Permission mode: default, accept-edits, auto, bypass-permissions")
	f.StringVar(&opts.workerAgent, "worker-agent", "", "Harness override for worker sessions")
	f.StringVar(&opts.orchestratorAgent, "orchestrator-agent", "", "Harness override for orchestrator sessions")
	f.StringVar(&opts.workerEffort, "worker-effort", "", "Reasoning effort for worker sessions (e.g. low, medium, high); empty clears it")
	f.StringVar(&opts.orchestratorEffort, "orchestrator-effort", "", "Reasoning effort for orchestrator sessions (e.g. low, medium, high); empty clears it")
	f.StringVar(&opts.reviewerEffort, "reviewer-effort", "", "Reasoning effort for every configured reviewer (set --reviewer first); empty clears it")
	f.StringVar(&opts.orchestratorResearchWindow, "orchestrator-research-window", "", "Compaction window of an orchestrator in research mode (`tallpine orchestrator research on`): a size from 50k to 1m or a percent of the model window; off disables research mode for the project; empty uses the default (250k)")
	f.StringVar(&opts.orchestratorIdleCompact, "orchestrator-idle-compact", "", "Context size above which an idle orchestrator compacts between tasks: a percent of its compaction window, not the model window (20%-95%), a size from 50k to 1m, or off; empty uses the default (30%)")
	f.StringVar(&opts.orchestratorIdleAction, "orchestrator-idle-action", "", "What an idle orchestrator past its idle threshold does: compact (default) or handoff to a fresh orchestrator that rebuilds its state from the daemon")
	f.StringVar(&opts.orchestratorCompactWindow, "orchestrator-compact-window", "", "Context size at which orchestrator sessions compact: a size from 50k to 1m, a percent of the model window (20%-95%), or off; empty uses the default (120k unless the project environment sets the window)")
	f.StringVar(&opts.agentRules, "agent-rules", "", "Project-specific standing instructions for worker sessions")
	f.StringVar(&opts.agentRulesFile, "agent-rules-file", "", "Repo-relative file containing worker standing instructions")
	f.StringVar(&opts.orchestratorRules, "orchestrator-rules", "", "Project-specific standing instructions for orchestrator sessions")
	f.StringArrayVar(&opts.env, "env", nil, "Env var KEY=VALUE forwarded into sessions (repeatable)")
	f.StringArrayVar(&opts.unsetEnv, "unset-env", nil, "Env var KEY to remove from the project's env (repeatable)")
	f.StringArrayVar(&opts.symlink, "symlink", nil, "Repo-relative path to symlink into workspaces (repeatable)")
	f.StringArrayVar(&opts.postCreate, "post-create", nil, "Command to run after workspace creation (repeatable)")
	f.BoolVar(&opts.trackerIntake, "tracker-intake", false, "Opt this project into issue intake (GitHub or GitLab; provider inferred from git origin). Inert unless the daemon also has TALLPINE_TRACKER_INTAKE=on")
	f.StringVar(&opts.trackerRepo, "tracker-repo", "", "Provider-native repo for issue intake (owner/repo or group/subgroup/repo; default: derive from git origin)")
	f.StringVar(&opts.trackerAssignee, "tracker-assignee", "", "Issue assignee required for intake eligibility")
	f.BoolVar(&opts.noCI, "no-ci", false, "Declare that the project has no CI: when the provider reports no checks for a PR head, an approved Tallpine review of that exact head is the merge gate (failing or pending checks still block); --no-ci=false restores the default")
	f.StringArrayVar(&opts.reviewers, "reviewer", nil, "Reviewer harness that reviews worker PRs (repeatable; e.g. claude-code)")
	f.BoolVar(&opts.planInPlace, "plan-in-place", false, "Let `tallpine spawn --backlog` start an implementer that plans in place when an item needs a plan, instead of a separate planner. A managed policy can lock it off; --plan-in-place=false restores the default")
	f.BoolVar(&opts.planAutoApprove, "plan-auto-approve", false, "Approve submitted plans automatically, without the owner, in this project. For unattended throwaway test or benchmark projects only. Only a person can set it (the daemon refuses it from agent sessions), and a managed policy can lock it off; --plan-auto-approve=false keeps it off in this project even when the app default is on (see --plan-auto-approve-inherit)")
	f.BoolVar(&opts.planAutoApproveInherit, "plan-auto-approve-inherit", false, "Clear this project's own plan auto-approve value so it follows the app default (Settings > Reviews)")
	f.StringVar(&opts.configJSON, "config-json", "", "Full config as a JSON object; replaces the entire config (overrides field flags)")
	f.BoolVar(&opts.clear, "clear", false, "Clear all config")
	f.BoolVar(&opts.json, "json", false, "Output the updated project as JSON")
	return cmd
}

// buildProjectConfig builds the full replacement config for the two replace
// modes: --clear empties the config and --config-json supplies the whole
// object. Field flags are not handled here; they merge into the stored config
// (see mergeProjectConfigFlags). The daemon validates the values.
func buildProjectConfig(opts projectSetConfigOptions) (map[string]any, error) {
	cfg := map[string]any{}
	if opts.clear {
		return cfg, nil
	}
	// Decode into a generic map, not projectConfig, so a key the CLI mirror
	// struct lacks is sent to the daemon instead of silently dropped.
	if err := json.Unmarshal([]byte(opts.configJSON), &cfg); err != nil {
		return nil, usageError{fmt.Errorf("--config-json is not a valid JSON object: %w", err)}
	}
	if cfg == nil {
		return nil, usageError{errors.New("--config-json is not a valid JSON object: got null")}
	}
	return cfg, nil
}

// projectConfigFieldFlags are the set-config flags that merge into the stored
// config.
var projectConfigFieldFlags = []string{
	"default-branch", "canonical-repo-url", "github-user", "session-prefix", "default-session-mode", "model", "permission",
	"worker-agent", "orchestrator-agent", "worker-effort", "orchestrator-effort", "reviewer-effort", "orchestrator-compact-window", "orchestrator-idle-compact", "orchestrator-research-window", "orchestrator-idle-action", "agent-rules", "agent-rules-file", "orchestrator-rules",
	"env", "unset-env", "symlink", "post-create", "tracker-intake", "tracker-repo", "tracker-assignee", "reviewer",
	"no-ci",
	"plan-auto-approve",
	"plan-auto-approve-inherit", "plan-in-place",
}

// mergeProjectConfigFlags fetches the project's current config as raw JSON and
// overlays only the flags the user explicitly set. Working on raw JSON keeps
// daemon fields the CLI mirror struct does not know about intact.
func (c *commandContext) mergeProjectConfigFlags(ctx context.Context, id string, changed func(string) bool, opts projectSetConfigOptions) (map[string]any, error) {
	anyChanged := false
	for _, name := range projectConfigFieldFlags {
		if changed(name) {
			anyChanged = true
			break
		}
	}
	if !anyChanged {
		return nil, usageError{errors.New("usage: provide at least one config flag, --config-json, or --clear")}
	}
	env, err := parseEnvPairs(opts.env)
	if err != nil {
		return nil, err
	}
	unset, err := parseEnvKeys(opts.unsetEnv)
	if err != nil {
		return nil, err
	}
	for _, key := range unset {
		if _, both := env[key]; both {
			return nil, usageError{fmt.Errorf("--env and --unset-env both name %q", key)}
		}
	}
	var current struct {
		Project struct {
			Config json.RawMessage `json:"config"`
		} `json:"project"`
	}
	if err := c.getJSON(ctx, "projects/"+url.PathEscape(id), &current); err != nil {
		return nil, err
	}
	cfg := map[string]any{}
	if len(current.Project.Config) > 0 && string(current.Project.Config) != "null" {
		if err := json.Unmarshal(current.Project.Config, &cfg); err != nil {
			return nil, fmt.Errorf("decode current project config: %w", err)
		}
	}
	if cfg == nil {
		cfg = map[string]any{}
	}

	if changed("default-branch") {
		setConfigString(cfg, "defaultBranch", opts.defaultBranch)
	}
	if changed("github-user") {
		setConfigString(cfg, "githubUser", opts.githubUser)
	}
	if changed("canonical-repo-url") {
		setConfigString(cfg, "canonicalRepoURL", opts.canonicalRepoURL)
	}
	if changed("session-prefix") {
		setConfigString(cfg, "sessionPrefix", opts.sessionPrefix)
	}
	if changed("default-session-mode") {
		switch opts.defaultSessionMode {
		case "", "tui", "chat":
		default:
			return nil, usageError{fmt.Errorf("--default-session-mode %q: want tui or chat", opts.defaultSessionMode)}
		}
		setConfigString(cfg, "defaultSessionMode", opts.defaultSessionMode)
	}
	if changed("agent-rules") {
		setConfigString(cfg, "agentRules", opts.agentRules)
	}
	if changed("agent-rules-file") {
		setConfigString(cfg, "agentRulesFile", opts.agentRulesFile)
	}
	if changed("orchestrator-rules") {
		setConfigString(cfg, "orchestratorRules", opts.orchestratorRules)
	}
	if changed("model") {
		setNestedConfigString(cfg, "agentConfig", "model", opts.model)
	}
	if changed("permission") {
		setNestedConfigString(cfg, "agentConfig", "permissions", opts.permission)
	}
	if changed("worker-agent") {
		setNestedConfigString(cfg, "worker", "agent", opts.workerAgent)
	}
	if changed("orchestrator-agent") {
		setNestedConfigString(cfg, "orchestrator", "agent", opts.orchestratorAgent)
	}
	for flag, v := range map[string]*string{"worker-effort": &opts.workerEffort, "orchestrator-effort": &opts.orchestratorEffort, "reviewer-effort": &opts.reviewerEffort} {
		if !changed(flag) {
			continue
		}
		// Store the canonical lowercase level: spawn compares effort case-sensitively.
		*v = strings.ToLower(strings.TrimSpace(*v))
		if *v != "" && !domain.ValidEffort(*v) {
			return nil, usageError{fmt.Errorf("--%s %q: want one of %s", flag, *v, strings.Join(domain.EffortOrder, ", "))}
		}
	}
	if changed("worker-effort") {
		setRoleEffort(cfg, "worker", opts.workerEffort)
	}
	if changed("orchestrator-effort") {
		setRoleEffort(cfg, "orchestrator", opts.orchestratorEffort)
	}
	if changed("orchestrator-compact-window") {
		setNestedConfigString(cfg, "orchestrator", "autoCompactWindow", opts.orchestratorCompactWindow)
	}
	if changed("orchestrator-idle-action") {
		setNestedConfigString(cfg, "orchestrator", "idleAction", opts.orchestratorIdleAction)
	}
	if changed("orchestrator-idle-compact") {
		setNestedConfigString(cfg, "orchestrator", "idleCompactAt", opts.orchestratorIdleCompact)
	}
	if changed("orchestrator-research-window") {
		setNestedConfigString(cfg, "orchestrator", "researchCompactWindow", opts.orchestratorResearchWindow)
	}
	if changed("tracker-repo") {
		setNestedConfigString(cfg, "trackerIntake", "repo", opts.trackerRepo)
	}
	if changed("tracker-assignee") {
		setNestedConfigString(cfg, "trackerIntake", "assignee", opts.trackerAssignee)
	}
	if changed("tracker-intake") {
		if opts.trackerIntake {
			nestedConfig(cfg, "trackerIntake")["enabled"] = true
		} else {
			deleteNestedConfig(cfg, "trackerIntake", "enabled")
		}
	}
	if changed("no-ci") {
		if opts.noCI {
			cfg["noCI"] = true
		} else {
			delete(cfg, "noCI")
		}
	}
	if changed("plan-auto-approve") {
		cfg["planAutoApprove"] = opts.planAutoApprove
	}
	if changed("plan-auto-approve-inherit") && opts.planAutoApproveInherit {
		delete(cfg, "planAutoApprove")
	}
	if changed("plan-in-place") {
		if opts.planInPlace {
			cfg["planInPlace"] = true
		} else {
			delete(cfg, "planInPlace")
		}
	}
	if changed("env") || changed("unset-env") {
		merged := nestedConfig(cfg, "env")
		for k, v := range env {
			merged[k] = v
		}
		for _, k := range unset {
			delete(merged, k)
		}
		pruneEmptyConfig(cfg, "env")
	}
	if changed("symlink") {
		setConfigList(cfg, "symlinks", opts.symlink)
	}
	if changed("post-create") {
		setConfigList(cfg, "postCreate", opts.postCreate)
	}
	if changed("reviewer") {
		setConfigReviewers(cfg, opts.reviewers)
	}
	if changed("reviewer-effort") {
		if err := setReviewerEffort(cfg, opts.reviewerEffort); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}
