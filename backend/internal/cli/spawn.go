package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

// maxDisplayNameLen caps the sidebar label set by `--name`. Mirrored by the
// daemon's spawn handler so a direct API call is held to the same limit.
const maxDisplayNameLen = 100

type spawnOptions struct {
	project         string
	standalone      bool
	harness         string
	kind            string
	mode            string
	branch          string
	prompt          string
	issue           string
	name            string
	model           string
	output          string
	role            string
	effort          string
	claimPR         string
	noTakeover      bool
	skipAgentCheck  bool
	trackerProvider string
	backlog         string
	backlogForce    bool
	plan            string
	allowDuplicate  bool
}

type spawnResult struct {
	Session struct {
		ID             string `json:"id"`
		Status         string `json:"status"`
		DisplayName    string `json:"displayName"`
		ProvisionState string `json:"provisionState"`
	} `json:"session"`
	PromptBytes       int `json:"promptBytes,omitempty"`
	SystemPromptBytes int `json:"systemPromptBytes,omitempty"`
	// Warnings are notices from the daemon, e.g. a backlog item with open blockers.
	Warnings []string `json:"warnings,omitempty"`
}

func newSpawnCommand(ctx *commandContext) *cobra.Command {
	var opts spawnOptions
	cmd := &cobra.Command{
		Use:   "spawn",
		Short: "Spawn an agent session",
		Long: "Spawn an agent session in a registered project, or a standalone worker with --standalone.\n\n" +
			"The session runs the chosen agent in a\n" +
			"fresh isolated workspace. Git projects use worktrees; standalone agents use a Tallpine-managed plain directory.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.standalone && strings.TrimSpace(opts.project) != "" {
				return usageError{fmt.Errorf("--standalone and --project cannot be used together")}
			}
			if opts.noTakeover && opts.claimPR == "" {
				return usageError{fmt.Errorf("--no-takeover requires --claim-pr")}
			}
			name := strings.TrimSpace(opts.name)
			if name == "" {
				return usageError{fmt.Errorf("--name is required")}
			}
			if utf8.RuneCountInString(name) > maxDisplayNameLen {
				return usageError{fmt.Errorf("--name must be %d characters or fewer", maxDisplayNameLen)}
			}

			// Rejected here rather than forwarded, so a typo exits 2 as a usage
			// error instead of reaching the daemon as an unsupported mode.
			if opts.mode != "" && opts.mode != "chat" && opts.mode != "tui" {
				return usageError{fmt.Errorf(`--mode must be "chat" or "tui"`)}
			}
			if opts.kind != "" && opts.kind != "worker" && opts.kind != "orchestrator" {
				return usageError{fmt.Errorf(`--kind must be "worker" or "orchestrator"`)}
			}
			if opts.output != "" && opts.output != "artifact" {
				return usageError{fmt.Errorf(`--output must be "artifact"`)}
			}
			if err := opts.validateBacklogFlags(); err != nil {
				return err
			}
			if opts.role != "" && opts.role != "planner" {
				return usageError{fmt.Errorf(`--role must be "planner"`)}
			}
			if opts.role != "" && (opts.kind == "orchestrator" || opts.standalone) {
				return usageError{fmt.Errorf("--role is for project worker sessions")}
			}
			if opts.role == "planner" {
				// A planner delivers through `tallpine plan`, never a PR.
				opts.output = "artifact"
			}
			if opts.output != "" && opts.kind == "orchestrator" {
				return usageError{fmt.Errorf("--output is supported for worker sessions only")}
			}
			if opts.standalone {
				if opts.kind == "orchestrator" {
					return usageError{fmt.Errorf("standalone sessions must be workers")}
				}
				if strings.TrimSpace(opts.branch) != "" || strings.TrimSpace(opts.issue) != "" || strings.TrimSpace(opts.claimPR) != "" {
					return usageError{fmt.Errorf("standalone sessions do not support --branch, --issue, or --claim-pr")}
				}
				if strings.TrimSpace(opts.harness) == "" {
					return usageError{fmt.Errorf("--agent is required with --standalone")}
				}
				opts.kind = "worker"
			}

			tp := strings.TrimSpace(opts.trackerProvider)
			if tp == "" {
				tp = "github"
			}
			if tp != "github" && tp != "gitlab" {
				return usageError{fmt.Errorf(`--tracker-provider must be "github" or "gitlab"`)}
			}
			opts.trackerProvider = tp

			var project projectDetails
			var err error
			if !opts.standalone {
				project, err = ctx.resolveSpawnProject(cmd.Context(), opts.project)
				if err != nil {
					return err
				}
				opts.project = project.ID
			}

			harness, err := resolveSpawnHarness(opts.harness, opts.kind, project)
			if err != nil {
				return err
			}
			if strings.TrimSpace(opts.harness) == "" {
				// Project config saved before a harness was removed may still name it.
				if resolved, replaced := domain.AgentHarness(harness).OrFallback(); replaced {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: project config names unsupported agent %q; using %s\n", harness, resolved)
					harness = string(resolved)
				}
			}
			opts.harness = harness

			if isScratchProject(project) {
				if strings.TrimSpace(opts.branch) != "" {
					return usageError{fmt.Errorf("scratch projects do not support --branch")}
				}
				if strings.TrimSpace(opts.claimPR) != "" {
					return usageError{fmt.Errorf("scratch projects do not support --claim-pr")}
				}
			}

			if !opts.skipAgentCheck {
				if err := ctx.preflightSpawnAgentAuth(cmd.Context(), cmd, opts.harness); err != nil {
					return err
				}
			}
			if err := ctx.validateModel(cmd.Context(), opts.harness, opts.model, project.ID); err != nil {
				return err
			}
			claimRef := ""
			if opts.claimPR != "" {
				claimRef, err = ctx.resolvePRRef(cmd.Context(), opts.claimPR, project)
				if err != nil {
					return err
				}
			}
			req := apitypes.SpawnSessionRequest{
				ProjectID:             domain.ProjectID(opts.project),
				IssueID:               domain.IssueID(opts.issue),
				ParentSessionID:       domain.SessionID(strings.TrimSpace(brand.Getenv("SESSION_ID"))),
				TrackerProvider:       domain.TrackerProvider(opts.trackerProvider),
				Kind:                  domain.SessionKind(opts.kind),
				Harness:               domain.AgentHarness(opts.harness),
				Mode:                  domain.SessionMode(opts.mode),
				Branch:                opts.branch,
				Prompt:                opts.prompt,
				Model:                 strings.TrimSpace(opts.model),
				Effort:                strings.TrimSpace(opts.effort),
				RequestedOutput:       domain.RequestedOutput(opts.output),
				Role:                  domain.SessionRole(opts.role),
				DisplayName:           name,
				BacklogRef:            strings.TrimSpace(opts.backlog),
				BacklogForce:          opts.backlogForce,
				BacklogPlan:           opts.plan,
				BacklogAllowDuplicate: opts.allowDuplicate,
			}
			var res spawnResult
			if err := ctx.postSetupJSON(cmd.Context(), "sessions", req, &res); err != nil {
				return err
			}
			if strings.TrimSpace(res.Session.ID) == "" {
				return fmt.Errorf("daemon returned empty session id for spawn")
			}
			claimed := ""
			var claim apitypes.ClaimPRResponse
			if opts.claimPR != "" {
				if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(res.Session.ID)+"/pr/claim", apitypes.ClaimPRRequest{PR: claimRef, AllowTakeover: ptrBool(!opts.noTakeover)}, &claim); err != nil {
					if killErr := ctx.rollbackSpawnedSession(cmd.Context(), res.Session.ID); killErr != nil {
						return fmt.Errorf("failed to claim PR %s: %w; rollback of session %s failed: %w", opts.claimPR, err, res.Session.ID, killErr)
					}
					return fmt.Errorf("failed to claim PR %s: %w; rolled back session %s", opts.claimPR, err, res.Session.ID)
				}
				if len(claim.PRs) > 0 {
					claimed = claim.PRs[0].URL
				}
			}
			out := cmd.OutOrStdout()
			claimLabel := ""
			if claimed != "" {
				claimLabel = fmt.Sprintf(" (claimed %s)", claimed)
			}
			promptSize := ""
			if res.PromptBytes > 0 || res.SystemPromptBytes > 0 {
				promptSize = fmt.Sprintf(" [prompt %d B, system %d B]", res.PromptBytes, res.SystemPromptBytes)
			}
			displayName := res.Session.DisplayName
			if displayName == "" {
				displayName = name
			}
			status := res.Session.Status
			if res.Session.ProvisionState == "provisioning" {
				// The agent starts after this answer; a start that fails later
				// is filed as an [auto] start_failed report and reads "exited".
				status = "starting"
			} else if status == "idle" && res.PromptBytes > 0 {
				// The task prompt was handed to the agent, which picks it up
				// moments later; "idle" would read as a worker with no work.
				status = "starting"
			}
			for _, warning := range res.Warnings {
				if _, err := fmt.Fprintf(out, "warning: %s\n", warning); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(out, "spawned session %s %q (%s)%s%s\n", res.Session.ID, displayName, status, claimLabel, promptSize); err != nil {
				return err
			}
			if opts.claimPR != "" {
				return writeClaimPRCheckout(out, claim.BranchChanged)
			}
			return nil
		},
	}
	f := cmd.Flags()
	// --agent is an alias for --harness so the more intuitive `tallpine spawn --agent
	// droid` works identically; both resolve to the same harness flag.
	f.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "agent" {
			name = "harness"
		}
		return pflag.NormalizedName(name)
	})
	f.StringVar(&opts.project, "project", "", "Project id to spawn the session in (default: TALLPINE_PROJECT_ID or the current registered repo)")
	f.BoolVar(&opts.standalone, "standalone", false, "Spawn a projectless worker in a Tallpine-managed plain directory (requires --agent)")
	f.StringVar(&opts.harness, "harness", "", "Agent harness / --agent: claude-code, codex, kiro, deepseek-harness (default: project worker.agent; orchestrator spawns default to project orchestrator.agent; required if the project has none)")
	f.StringVar(&opts.kind, "kind", "", "Session role: worker or orchestrator (default: worker)")
	f.StringVar(&opts.mode, "mode", "", "Initial session interface: chat (structured agent connection) or tui (the agent's native terminal). Omitted uses the daemon default; compatible sessions can switch later.")
	f.StringVar(&opts.branch, "branch", "", "Branch for git project sessions (default: tp/<session-id>/root; unsupported for standalone or Scratch sessions)")
	f.StringVar(&opts.prompt, "prompt", "", "Initial prompt for the agent")
	f.StringVar(&opts.model, "model", "", "Agent model override for this session only (exact ID; see tallpine agent models); overrides project/role config without changing it")
	f.StringVar(&opts.output, "output", "", "Deliverable for the session: artifact asks the agent to write a file in its artifact directory instead of opening a PR (default: ordinary task)")
	f.StringVar(&opts.role, "role", "", "Special worker role: planner starts a read-only planning session that submits through `tallpine plan` (implies --output artifact)")
	f.StringVar(&opts.effort, "effort", "", "Reasoning effort override for this session only (e.g. low, medium, high); empty defers to the agent/model default")
	f.StringVar(&opts.issue, "issue", "", "Issue id to associate with the session")
	f.StringVar(&opts.backlog, "backlog", "", "Backlog item ref (e.g. B12) to work on: its title, note, done-when and agent notes join the task, and the session is linked to it")
	f.BoolVar(&opts.allowDuplicate, "allow-duplicate", false, "With --backlog, start the item even if a live session already holds it (--force does not imply this)")
	f.StringVar(&opts.plan, "plan", "", "With --backlog, who plans an item that needs a plan: in-place (the spawned implementer writes the plan, waits read-only for approval, then builds; needs the project's planInPlace setting) or separate (a planner session via `tallpine plan start`). Default: follows the project's planInPlace setting")
	f.BoolVar(&opts.backlogForce, "force", false, "With --backlog, start the item even if it is held or has an open blocker")
	f.StringVar(&opts.trackerProvider, "tracker-provider", "github", "Issue tracker provider: github or gitlab (default: github)")
	f.StringVar(&opts.name, "name", "", "Display name shown in the sidebar (required, max 100 characters)")
	f.StringVar(&opts.claimPR, "claim-pr", "", "Claim PR ownership metadata only for the spawned session; does not check out the PR branch")
	f.BoolVar(&opts.noTakeover, "no-takeover", false, "Refuse if another active session owns the claimed PR (requires --claim-pr)")
	f.BoolVar(&opts.skipAgentCheck, "skip-agent-check", false, "Skip CLI readiness warnings (the daemon still validates launch readiness)")
	return cmd
}

// isAgentScopeForbidden reports whether err is the daemon refusing a route to
// a scoped agent token (for example a worker calling an orchestrator-only route).
func isAgentScopeForbidden(err error) bool {
	var apiErr apiResponseError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden && apiErr.ErrorBody.Code == "AGENT_SCOPE_FORBIDDEN"
}

// readOnlyAgentInventory is the worker fallback: the cached catalog from the
// worker-visible GET /agents, without forcing a readiness check.
func (c *commandContext) readOnlyAgentInventory(ctx context.Context, cmd *cobra.Command) (agentInventory, error) {
	var inventory agentInventory
	if err := c.getJSON(ctx, "agents", &inventory); err != nil {
		return agentInventory{}, err
	}
	if cmd != nil {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "note: this role cannot refresh agent readiness; showing the daemon's cached catalog. Ask an orchestrator or a human to refresh readiness."); err != nil {
			return agentInventory{}, err
		}
	}
	return inventory, nil
}

func (c *commandContext) fetchAgentInventory(ctx context.Context, cmd *cobra.Command, refresh bool) (agentInventory, error) {
	if refresh {
		var inventory agentInventory
		if err := c.postJSON(ctx, "agents/refresh", struct{}{}, &inventory); err != nil {
			if isAgentScopeForbidden(err) {
				return c.readOnlyAgentInventory(ctx, cmd)
			}
			return agentInventory{}, err
		}
		return inventory, nil
	}
	readiness, err := c.ensureAgentReadiness(ctx, nil, "display")
	if err != nil {
		if isAgentScopeForbidden(err) {
			return c.readOnlyAgentInventory(ctx, cmd)
		}
		return agentInventory{}, err
	}
	return readinessInventory(readiness), nil
}

func (c *commandContext) resolveSpawnProject(ctx context.Context, explicit string) (projectDetails, error) {
	if id := strings.TrimSpace(explicit); id != "" {
		return c.fetchProjectDetails(ctx, id)
	}
	if id := strings.TrimSpace(brand.Getenv("PROJECT_ID")); id != "" {
		return c.fetchProjectDetails(ctx, id)
	}
	if sessionID := strings.TrimSpace(brand.Getenv("SESSION_ID")); sessionID != "" {
		project, err := c.resolveProjectFromSession(ctx, sessionID)
		if err != nil {
			return projectDetails{}, err
		}
		return project, nil
	}
	project, ok, err := c.resolveProjectFromCWD(ctx)
	if err != nil {
		return projectDetails{}, err
	}
	if ok {
		return project, nil
	}
	return projectDetails{}, usageError{fmt.Errorf("project could not be resolved; pass --project, use --standalone, or run `tallpine project add --path <repo-path> --worker-agent <agent>`")}
}

func (c *commandContext) resolveProjectFromSession(ctx context.Context, sessionID string) (projectDetails, error) {
	sess, err := c.fetchScopedSession(ctx, sessionID, "")
	if err != nil {
		return projectDetails{}, usageError{fmt.Errorf("project could not be resolved from TALLPINE_SESSION_ID %q; pass --project", sessionID)}
	}
	if strings.TrimSpace(string(sess.ProjectID)) == "" {
		return projectDetails{}, usageError{fmt.Errorf("project could not be resolved from TALLPINE_SESSION_ID %q; pass --project", sessionID)}
	}
	return c.fetchProjectDetails(ctx, string(sess.ProjectID))
}

func (c *commandContext) resolveProjectFromCWD(ctx context.Context) (projectDetails, bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return projectDetails{}, false, err
	}
	cwd, err = normalizeProjectMatchPath(cwd)
	if err != nil {
		return projectDetails{}, false, err
	}

	var list projectListResult
	if err := c.getJSON(ctx, "projects", &list); err != nil {
		return projectDetails{}, false, err
	}
	sort.Slice(list.Projects, func(i, j int) bool {
		return list.Projects[i].ID < list.Projects[j].ID
	})

	var best projectDetails
	bestLen := -1
	ambiguous := false
	for _, summary := range list.Projects {
		project, err := c.fetchProjectDetails(ctx, summary.ID)
		if err != nil {
			return projectDetails{}, false, err
		}
		if project.Path == "" {
			continue
		}
		projectPath, err := normalizeProjectMatchPath(project.Path)
		if err != nil {
			continue
		}
		if !pathx.Within(projectPath, cwd) {
			continue
		}
		pathLen := len(projectPath)
		switch {
		case pathLen > bestLen:
			best = project
			bestLen = pathLen
			ambiguous = false
		case pathLen == bestLen:
			ambiguous = true
		}
	}
	if bestLen == -1 {
		return projectDetails{}, false, nil
	}
	if ambiguous {
		return projectDetails{}, false, usageError{fmt.Errorf("current directory matches multiple registered projects; pass --project")}
	}
	return best, true, nil
}

func isScratchProject(project projectDetails) bool {
	return project.ID == "scratch" && project.Kind == "scratch"
}

func normalizeProjectMatchPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if realPath, err := filepath.EvalSymlinks(abs); err == nil {
		abs = realPath
	}
	return filepath.Clean(abs), nil
}

func (c *commandContext) preflightSpawnAgentAuth(ctx context.Context, cmd *cobra.Command, agentID string) error {
	readiness, err := c.ensureAgentReadiness(ctx, []string{agentID}, "launch")
	if err != nil {
		if isAgentScopeForbidden(err) {
			_, werr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: this role cannot check agent %q readiness; continuing and letting spawn validate runtime readiness\n", agentID)
			return werr
		}
		var apiErr apiResponseError
		if errors.As(err, &apiErr) && apiErr.ErrorBody.Code == "UNKNOWN_AGENT_ID" {
			return fmt.Errorf("agent %q is not supported by this daemon; pass a supported --agent or run `tallpine agent ls`", agentID)
		}
		return err
	}
	if len(readiness.Agents) != 1 {
		return fmt.Errorf("agent %q is not supported by this daemon; pass a supported --agent or run `tallpine agent ls`", agentID)
	}
	snapshot := readiness.Agents[0]
	if snapshot.Installation.State == "not_installed" {
		return fmt.Errorf("agent %q needs install; install the agent CLI before spawning", agentID)
	}
	if snapshot.Installation.State == "unknown" {
		_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: agent %q installation status is unknown; continuing and letting spawn validate runtime readiness\n", agentID)
		return err
	}
	if snapshot.Authentication.State == "authorized" || snapshot.Authentication.State == "not_applicable" {
		return nil
	}
	if snapshot.Authentication.State == "unauthorized" {
		_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: agent %q may need auth according to daemon readiness; continuing and letting spawn validate runtime readiness\n", agentID)
		return err
	}
	_, err = fmt.Fprintf(cmd.ErrOrStderr(), "warning: agent %q auth status is unknown; continuing and letting spawn validate runtime readiness\n", agentID)
	return err
}

func (c *commandContext) ensureAgentReadiness(ctx context.Context, agentIDs []string, purpose string) (agentReadinessResponse, error) {
	var result agentReadinessResponse
	request := apitypes.EnsureAgentReadinessRequest{AgentIDs: agentIDs, Purpose: domain.AgentReadinessPurpose(purpose)}
	if err := c.postJSON(ctx, "agents/readiness/ensure", request, &result); err != nil {
		return agentReadinessResponse{}, err
	}
	return result, nil
}

// rollbackSpawnedSession reverses a partial `spawn` whose out-of-band follow-up
// (PR claim) failed. It calls the daemon's `/rollback` endpoint, which deletes
// the seed-state row outright instead of marking it terminated — so the user
// does not see an orphan terminated session under `--include-terminated`. If
// spawn output has already landed (workspace + runtime), the daemon falls back
// to a Kill on the server side so teardown still happens.
func (c *commandContext) rollbackSpawnedSession(ctx context.Context, id string) error {
	var res apitypes.RollbackSessionResponse
	return c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/rollback", struct{}{}, &res)
}
