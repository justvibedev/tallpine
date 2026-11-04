package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// The session retention policy is the sessionRetention agent-policy rule; the
// cleanup engine's session_retention category applies it. These commands are
// a focused view over both endpoints.
const (
	retentionRule     = "sessionRetention"
	retentionCategory = "session_retention"
)

func newRetentionCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Show, change, preview and run the session retention policy",
		Long: "The session retention policy purges data of ended sessions older than an age threshold: raw provider events, " +
			"worktrees (dirty ones are salvaged first) and, optionally, transcripts. It is on by default. Usage, cost, turn counts, " +
			"compaction markers, reviews and PRs are always kept, and live sessions are never touched. Orchestrator sessions are excluded unless " +
			"includeOrchestrators is on, and the shared orchestrator worktree is never removed. A session whose events or " +
			"transcript were purged can no longer be restored.\n\n" +
			"A value set by managed config (MDM) or a TALLPINE_AGENT_* variable is locked and cannot be changed here. " +
			"See docs/agent-policy.md.",
		Example: `  # Preview what the policy would remove and the space it frees
  tallpine retention preview

  # Opt in: purge ended sessions after 7 days
  tallpine retention set enabled=true days=7

  # Keep one project's data longer
  tallpine retention set --project my-app days=90

  # Apply it now instead of waiting for the daily pass
  tallpine retention run --yes`,
		Args: noArgs,
	}
	cmd.AddCommand(newRetentionShowCommand(ctx), newRetentionSetCommand(ctx), newRetentionPreviewCommand(ctx), newRetentionRunCommand(ctx))
	return cmd
}

func agentPolicyPath(project string) string {
	if project != "" {
		return "projects/" + url.PathEscape(project) + "/agent-policy"
	}
	return "settings/agent-policy"
}

func newRetentionShowCommand(ctx *commandContext) *cobra.Command {
	var project string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the effective retention settings and which layer sets each",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var p apitypes.AgentPolicyResponse
			if err := ctx.getJSON(cmd.Context(), agentPolicyPath(project), &p); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, rule := range p.Rules {
				if rule.Name != retentionRule {
					continue
				}
				if asJSON {
					return writeJSON(out, rule)
				}
				for _, f := range rule.Fields {
					value, _ := json.Marshal(f.Value)
					lock := ""
					if f.Locked {
						lock = ", locked"
					}
					if _, err := fmt.Fprintf(out, "%s: %s (%s%s)\n", f.Name, value, f.Source, lock); err != nil {
						return err
					}
				}
				return nil
			}
			return errors.New("this daemon has no session retention policy; update Tallpine")
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Show the settings as they apply to this project")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newRetentionSetCommand(ctx *commandContext) *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:   "set <field>=<value>...",
		Short: "Change retention settings (enabled, days, dropWorktrees, dropTranscripts, includeOrchestrators)",
		Long: "Changes the user's retention settings, or with --project that project's override. Fields: enabled (true/false), " +
			"days (1-3650, default 7), dropWorktrees, dropTranscripts, includeOrchestrators (true/false). dropProviderEvents is deprecated and ignored: raw provider events follow app.providerEventRetentionDays (Settings > Storage). A setting locked by managed config is refused.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fields, err := retentionPatch(args)
			if err != nil {
				return usageError{err}
			}
			if _, ok := fields["dropProviderEvents"]; ok {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "note: dropProviderEvents is deprecated and ignored; raw provider events follow app.providerEventRetentionDays (Settings > Storage).")
			}
			body := map[string]any{"rules": map[string]any{retentionRule: fields}}
			if err := ctx.patchJSON(cmd.Context(), agentPolicyPath(project), body, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "saved")
			return err
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Change this project's override instead of the user setting")
	return cmd
}

// retentionPatch parses field=value pairs into typed values.
func retentionPatch(args []string) (map[string]any, error) {
	fields := map[string]any{}
	for _, arg := range args {
		name, raw, ok := strings.Cut(arg, "=")
		name, raw = strings.TrimSpace(name), strings.TrimSpace(raw)
		if !ok || name == "" || raw == "" {
			return nil, fmt.Errorf("%q: expected field=value", arg)
		}
		switch name {
		case "days":
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("days must be a whole number, got %q", raw)
			}
			fields[name] = n
		case "enabled", "dropProviderEvents", "dropWorktrees", "dropTranscripts", "includeOrchestrators":
			switch strings.ToLower(raw) {
			case "true", "on", "yes", "1":
				fields[name] = true
			case "false", "off", "no", "0":
				fields[name] = false
			default:
				return nil, fmt.Errorf("%s must be true or false, got %q", name, raw)
			}
		default:
			return nil, fmt.Errorf("unknown field %q (enabled, days, dropProviderEvents, dropWorktrees, dropTranscripts, includeOrchestrators)", name)
		}
	}
	return fields, nil
}

func newRetentionPreviewCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Show what the policy would remove and how much space it frees, without removing anything",
		Long: "A dry run of the session retention policy. It previews with the current settings even while the policy is off, " +
			"so you can see the effect before opting in.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.runCleanup(cmd.Context(), cmd, cleanupOptions{dryRun: true, json: asJSON, only: []string{retentionCategory}})
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the report as JSON")
	return cmd
}

func newRetentionRunCommand(ctx *commandContext) *cobra.Command {
	var opts cleanupOptions
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Apply the retention policy now (shows a preview and asks first)",
		Long: "Applies the session retention policy now instead of waiting for the daily cleanup pass. Only projects where the " +
			"policy is on are purged. Without --yes it shows the preview and asks for confirmation.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.json && !opts.yes {
				return usageError{errors.New("--json needs --yes (it cannot prompt)")}
			}
			opts.only = []string{retentionCategory}
			return ctx.runCleanup(cmd.Context(), cmd, opts)
		},
	}
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the run report as JSON")
	return cmd
}
