package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/officialimport"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

type importOfficialOptions struct {
	projects    []string
	all         bool
	dryRun      bool
	settings    bool
	fromDataDir string
	json        bool
}

type importOfficialResponse struct {
	Report officialimport.Report `json:"report"`
}

func newImportCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import data from another install",
	}
	cmd.AddCommand(newImportAOCommand(ctx))
	return cmd
}

func newImportAOCommand(ctx *commandContext) *cobra.Command {
	var opts importOfficialOptions
	cmd := &cobra.Command{
		Use:   "ao",
		Short: "Import projects and settings from the official Agent Orchestrator",
		Long: "Import registered projects (path, kind, workspace child repos, repo URLs, default branch and the full " +
			"per-project config) and, with --settings, the global non-secret settings that map one-to-one, from an " +
			"official Agent Orchestrator install (" + "~/" + brand.OfficialHomeDirName + "/data) into this one.\n\n" +
			"The official install is only ever READ: its database is cloned (copy-on-write) into this product's own " +
			"data dir and the clone is read, so the official app can keep running. Sessions, chat history, reports, " +
			"artifacts, browser data and every token or secret are never imported.\n\n" +
			"Harnesses this product no longer ships are normalized to claude-code with a warning. The command is " +
			"idempotent: re-running updates changed projects and skips unchanged ones. Use --dry-run to print the plan, " +
			"and --project <id> (repeatable) to migrate gradually.",
		Example: "  tallpine import ao --dry-run\n" +
			"  tallpine import ao --project my-app --project my-lib\n" +
			"  tallpine import ao --all --settings",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runImportOfficial(cmd, opts)
		},
	}
	cmd.Flags().StringArrayVar(&opts.projects, "project", nil, "Source project id to import (repeatable)")
	cmd.Flags().BoolVar(&opts.all, "all", false, "Import every active project (required for a real run without --project)")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Print the plan without writing anything")
	cmd.Flags().BoolVar(&opts.settings, "settings", false, "Also import the global settings that map one-to-one")
	cmd.Flags().StringVar(&opts.fromDataDir, "from-data-dir", "", "Official data dir to read (default ~/"+brand.OfficialHomeDirName+"/data)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the report as JSON")
	return cmd
}

func (c *commandContext) runImportOfficial(cmd *cobra.Command, opts importOfficialOptions) error {
	if opts.all && len(opts.projects) > 0 {
		return usageError{errors.New("--all and --project are mutually exclusive")}
	}
	if !opts.dryRun && !opts.all && len(opts.projects) == 0 {
		return usageError{errors.New("choose what to import: --project <id> (repeatable) or --all; add --dry-run to preview")}
	}
	req := apitypes.AOImportRequest{DryRun: opts.dryRun, ProjectIDs: opts.projects, IncludeSettings: opts.settings}
	if strings.TrimSpace(opts.fromDataDir) != "" {
		dir, err := pathx.ExpandHome(opts.fromDataDir)
		if err != nil {
			return err
		}
		if req.SourceDataDir, err = resolvedPath(dir); err != nil {
			return err
		}
	}
	var resp importOfficialResponse
	if err := c.postJSON(cmd.Context(), "imports/ao", req, &resp); err != nil {
		return err
	}
	rep := resp.Report
	if opts.json {
		if err := writeJSON(cmd.OutOrStdout(), rep); err != nil {
			return err
		}
	} else if err := writeImportOfficialSummary(cmd.OutOrStdout(), rep, len(opts.projects) > 0); err != nil {
		return err
	}
	if !rep.Found {
		return fmt.Errorf("no Agent Orchestrator database found in %s", rep.SourceDataDir)
	}
	if rep.HasFailures() {
		return fmt.Errorf("%d project(s) could not be imported", rep.Counts.Error+rep.Counts.Conflict)
	}
	return nil
}

func writeImportOfficialSummary(w io.Writer, rep officialimport.Report, explicit bool) error {
	var b strings.Builder
	if rep.DryRun {
		b.WriteString("Dry run -- nothing was written.\n")
	}
	fmt.Fprintf(&b, "Source: %s (read-only)\n", rep.SourceDataDir)
	if !rep.Found {
		b.WriteString("No Agent Orchestrator database found there.\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	b.WriteString("\nProjects:\n")
	for _, p := range rep.Projects {
		label := string(p.Action)
		switch {
		case !p.Selected && p.Action != officialimport.ActionSkip:
			label = "not selected"
		case p.Applied:
			label += " (done)"
		}
		kind := ""
		if p.Kind == "workspace" {
			kind = fmt.Sprintf(" [workspace, %d repos]", p.ChildRepos)
		}
		fmt.Fprintf(&b, "  %-18s %s  %s%s\n", label, p.ID, p.Path, kind)
		if p.Name != "" && p.Name != p.ID {
			fmt.Fprintf(&b, "  %-18s name: %s\n", "", p.Name)
		}
		if p.Reason != "" {
			fmt.Fprintf(&b, "  %-18s %s", "", p.Reason)
			if p.TargetID != "" {
				fmt.Fprintf(&b, " [target: %s %s]", p.TargetID, p.TargetPath)
			}
			b.WriteByte('\n')
		}
		if p.Error != "" {
			fmt.Fprintf(&b, "  %-18s error: %s\n", "", p.Error)
		}
		if len(p.ConfigItems) > 0 && p.Selected {
			fmt.Fprintf(&b, "  %-18s config: %s\n", "", strings.Join(p.ConfigItems, ", "))
		}
		for _, warn := range p.Warnings {
			fmt.Fprintf(&b, "  %-18s warning: %s\n", "", warn)
		}
	}

	if len(rep.Settings) > 0 {
		b.WriteString("\nGlobal settings")
		if !rep.IncludeSettings {
			b.WriteString(" (not applied; pass --settings to import them)")
		}
		b.WriteString(":\n")
		for _, s := range rep.Settings {
			state := string(s.Action)
			if s.Applied {
				state += " (done)"
			}
			fmt.Fprintf(&b, "  %-18s %s = %s", state, s.Key, s.Value)
			if s.Action == officialimport.ActionUpdate && s.Current != "" {
				fmt.Fprintf(&b, " (now %s)", s.Current)
			}
			b.WriteByte('\n')
			if s.Error != "" {
				fmt.Fprintf(&b, "  %-18s error: %s\n", "", s.Error)
			}
		}
	}
	for _, warn := range rep.Warnings {
		fmt.Fprintf(&b, "\nwarning: %s", warn)
	}
	if len(rep.Warnings) > 0 {
		b.WriteByte('\n')
	}

	b.WriteString("\nNot imported:\n")
	for _, s := range rep.Skipped {
		count := ""
		if s.Count > 0 {
			count = fmt.Sprintf(" (%d)", s.Count)
		}
		fmt.Fprintf(&b, "  - %s%s: %s\n", s.What, count, s.Why)
	}

	c := rep.Counts
	fmt.Fprintf(&b, "\nSummary: %d create, %d update, %d unchanged, %d conflict, %d skipped, %d error\n",
		c.Create, c.Update, c.Unchanged, c.Conflict, c.Skip, c.Error)
	if rep.DryRun && (c.Create+c.Update) > 0 {
		cmdline := "tallpine import ao --all"
		if explicit {
			cmdline = "tallpine import ao --project <id>"
		}
		fmt.Fprintf(&b, "Run `%s` (without --dry-run) to apply.\n", cmdline)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
