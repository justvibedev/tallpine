package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func writeProjectList(cmd *cobra.Command, projects []projectSummary) error {
	out := cmd.OutOrStdout()
	if len(projects) == 0 {
		if _, err := fmt.Fprintln(out, "No projects registered."); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Run `tallpine project add --path <path>` to register one.")
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tNAME\tKIND\tSESSION PREFIX\tSTATUS"); err != nil {
		return err
	}
	for _, p := range projects {
		status := "ok"
		if p.ResolveError != "" {
			status = "degraded: " + p.ResolveError
		}
		kind := p.Kind
		if kind == "" {
			kind = "single_repo"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, kind, p.SessionPrefix, status); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func writeProjectDetails(cmd *cobra.Command, res projectGetResult) error {
	out := cmd.OutOrStdout()
	p := res.Project
	if _, err := fmt.Fprintf(out, "Project %s (%s)\n", p.ID, res.Status); err != nil {
		return err
	}
	fields := []struct {
		label string
		value string
	}{
		{label: "name", value: p.Name},
		{label: "kind", value: p.Kind},
		{label: "path", value: p.Path},
		{label: "repo", value: p.Repo},
		{label: "default branch", value: p.DefaultBranch},
		{label: "agent", value: p.Agent},
		{label: "github account", value: p.GitHubAccount},
		{label: "config", value: formatProjectConfig(p.Config)},
		{label: "resolve error", value: p.ResolveError},
	}
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		if _, err := fmt.Fprintf(out, "  %s: %s\n", f.label, f.value); err != nil {
			return err
		}
	}
	if len(p.WorkspaceRepos) > 0 {
		if _, err := fmt.Fprintln(out, "  workspace repos:"); err != nil {
			return err
		}
		for _, repo := range p.WorkspaceRepos {
			desc := repo.RelativePath
			if repo.Repo != "" {
				desc += " (" + repo.Repo + ")"
			}
			if _, err := fmt.Fprintf(out, "    %s: %s\n", repo.Name, desc); err != nil {
				return err
			}
		}
	}
	return nil
}

// formatProjectConfig renders the per-project config as compact JSON for the
// `project get` text view. A nil config returns "" so the row is skipped.
func formatProjectConfig(config *projectConfig) string {
	if config == nil {
		return ""
	}
	data, err := json.Marshal(config)
	if err != nil {
		return ""
	}
	return string(data)
}

func confirmProjectRemoval(cmd *cobra.Command, id string) (bool, error) {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Remove project %q? Type the project id to confirm: ", id); err != nil {
		return false, err
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	return strings.TrimSpace(line) == id, nil
}
