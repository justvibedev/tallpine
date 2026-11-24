package cli

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

func newSettingsApplyRoleDefaultCommand(ctx *commandContext) *cobra.Command {
	var asJSON, yes bool
	var projects []string
	cmd := &cobra.Command{
		Use:   "apply-role-default <orchestrator|worker|reviewer>",
		Short: "Bring every project's role in line with the app default model and effort",
		Long: "Lists the projects whose orchestrator, worker or reviewer would not run with the app-wide default " +
			"agent, model and effort (app.workflowDefaults.*), and with --yes rewrites them: a project's own model " +
			"and effort are cleared so it inherits the default, or set to the default when a project-wide value " +
			"would still win; a role on another agent is switched to the default's agent. --project limits it to " +
			"those projects. Without --yes nothing changes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role := domain.DefaultRole(args[0])
			if !role.Valid() {
				return fmt.Errorf("role must be orchestrator, worker or reviewer, not %q", args[0])
			}
			base := "settings/role-defaults/" + url.PathEscape(string(role))
			out := cmd.OutOrStdout()
			if !yes {
				var report projectsvc.RoleDefaultDriftReport
				if err := ctx.getJSON(cmd.Context(), base+"/drift", &report); err != nil {
					return err
				}
				report.Projects = filterDrift(report.Projects, projects)
				if asJSON {
					return writeJSON(out, report)
				}
				return printRoleDrift(out, report)
			}
			in := projectsvc.ApplyRoleDefaultInput{}
			for _, p := range projects {
				in.ProjectIDs = append(in.ProjectIDs, domain.ProjectID(p))
			}
			var result projectsvc.ApplyRoleDefaultResult
			if err := ctx.postJSON(cmd.Context(), base+"/apply", in, &result); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(out, result)
			}
			return printRoleApply(out, result)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Apply the default to the listed projects (without it, only list them)")
	cmd.Flags().StringSliceVar(&projects, "project", nil, "Only these projects (repeatable or comma-separated)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func filterDrift(in []projectsvc.RoleDefaultDrift, only []string) []projectsvc.RoleDefaultDrift {
	if len(only) == 0 {
		return in
	}
	keep := map[string]bool{}
	for _, p := range only {
		keep[p] = true
	}
	out := []projectsvc.RoleDefaultDrift{}
	for _, d := range in {
		if keep[string(d.ProjectID)] {
			out = append(out, d)
		}
	}
	return out
}

func formatRoleValue(v projectsvc.RoleValue) string {
	parts := []string{orAgentDefault(v.Agent), orAgentDefault(v.Model), orAgentDefault(v.Effort)}
	return strings.Join(parts, " / ")
}

func orAgentDefault(s string) string {
	if s == "" {
		return "agent default"
	}
	return s
}

func printRoleDrift(out io.Writer, report projectsvc.RoleDefaultDriftReport) error {
	if _, err := fmt.Fprintf(out, "App default %s: %s\n", report.Role, formatRoleValue(report.Default)); err != nil {
		return err
	}
	if len(report.Projects) == 0 {
		_, err := fmt.Fprintln(out, "Every project already runs with it.")
		return err
	}
	for _, d := range report.Projects {
		if _, err := fmt.Fprintf(out, "  %s  %s  (%s)\n", d.ProjectID, formatRoleValue(d.Current), d.Change); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "Run again with --yes to apply the default to these %d project(s).\n", len(report.Projects))
	return err
}

func printRoleApply(out io.Writer, result projectsvc.ApplyRoleDefaultResult) error {
	if _, err := fmt.Fprintf(out, "Applied the app default %s to %d project(s).\n", result.Role, len(result.Applied)); err != nil {
		return err
	}
	for _, f := range result.Failed {
		if _, err := fmt.Fprintf(out, "  failed %s: %s\n", f.ProjectID, f.Error); err != nil {
			return err
		}
	}
	if len(result.Failed) > 0 {
		return fmt.Errorf("%d project(s) could not be changed", len(result.Failed))
	}
	return nil
}
