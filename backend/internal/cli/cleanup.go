package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// cleanupRunTimeout bounds a manual run: it measures and removes whole
// worktrees and artifact trees, which can take minutes on a large install.
const cleanupRunTimeout = 30 * time.Minute

type cleanupOptions struct {
	dryRun           bool
	light            bool
	confirmArtifacts bool
	yes              bool
	json             bool
	// only limits the run to these categories (tallpine retention run).
	only []string
}

func newCleanupCommand(ctx *commandContext) *cobra.Command {
	var opts cleanupOptions
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove what ended agent sessions left behind",
		Long: "Run a cleanup pass now: worktrees of terminated sessions whose work is merged or pushed (never ones with uncommitted changes, " +
			"an operation in progress, stashes, unpushed commits, or used by a live session) and artifacts of sessions that ended longer ago " +
			"than the retention window. Artifacts (version history, tool directories and deliverables) only go with --confirm-artifacts, " +
			"unless the policy's autoPrune/autoDeliverables is on.\n\n" +
			"The daemon also runs a light pass hourly and a full pass daily while automatic cleanup is on (see `tallpine cleanup policy`). " +
			"Its first scheduled pass is a dry run that only reports.",
		Example: `  # See what a pass would remove
  tallpine cleanup --dry-run

  # Run a full pass without the prompt
  tallpine cleanup --yes

  # Also remove the artifact history of long-finished sessions and archive their deliverables
  tallpine cleanup --confirm-artifacts --yes`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if opts.json && !opts.dryRun && !opts.yes {
				return usageError{errors.New("--json needs --dry-run or --yes (it cannot prompt)")}
			}
			return ctx.runCleanup(cmd.Context(), cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Report what would be removed without removing anything")
	cmd.Flags().BoolVar(&opts.light, "light", false, "Run the light pass (worktrees, session temp dirs and containers) instead of the full pass")
	cmd.Flags().BoolVar(&opts.confirmArtifacts, "confirm-artifacts", false, "Archive or delete (per policy) deliverables of sessions past retention")
	cmd.Flags().BoolVarP(&opts.yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output the run report as JSON")
	cmd.AddCommand(newCleanupStatusCommand(ctx), newCleanupHistoryCommand(ctx), newCleanupPolicyCommand(ctx))
	return cmd
}

func (c *commandContext) postCleanupRun(ctx context.Context, req apitypes.CleanupRunRequest) (apitypes.CleanupRunReport, error) {
	var out apitypes.CleanupRunReport
	err := c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodPost, "/api/v1/cleanup/runs", req, &out, nil, cleanupRunTimeout)
	return out, err
}

func (c *commandContext) runCleanup(ctx context.Context, cmd *cobra.Command, opts cleanupOptions) error {
	out := cmd.OutOrStdout()
	pass := "full"
	if opts.light {
		pass = "light"
	}
	req := apitypes.CleanupRunRequest{Pass: pass, ConfirmArtifacts: opts.confirmArtifacts, Only: opts.only}
	if !opts.yes || opts.dryRun {
		preview := req
		preview.DryRun = true
		report, err := c.postCleanupRun(ctx, preview)
		if err != nil {
			return err
		}
		if opts.dryRun {
			if opts.json {
				return writeJSON(out, report)
			}
			return printCleanupReport(out, report)
		}
		if err := printCleanupReport(out, report); err != nil {
			return err
		}
		if cleanupWouldFree(report) == 0 {
			_, err := fmt.Fprintln(out, "\nNothing to remove.")
			return err
		}
		confirmed, err := confirmCleanup(cmd, report)
		if err != nil {
			return err
		}
		if !confirmed {
			_, err := fmt.Fprintln(out, "aborted")
			return err
		}
	}
	report, err := c.postCleanupRun(ctx, req)
	if err != nil {
		return err
	}
	if opts.json {
		return writeJSON(out, report)
	}
	return printCleanupReport(out, report)
}

func cleanupWouldFree(r apitypes.CleanupRunReport) int64 {
	var total int64
	for _, cat := range r.Categories {
		total += cat.WouldFreeBytes
	}
	return total
}

func confirmCleanup(cmd *cobra.Command, preview apitypes.CleanupRunReport) (bool, error) {
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "\nRemove %s now? Type yes to confirm: ", humanBytes(cleanupWouldFree(preview))); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}

var cleanupStatusLabel = map[string]string{
	"freed": "freed", "would_free": "would free", "needs_confirmation": "needs confirmation", "kept": "kept", "failed": "failed",
}

func printCleanupReport(w io.Writer, r apitypes.CleanupRunReport) error {
	kind := r.Pass + " pass"
	if r.DryRun {
		kind += ", dry run"
	}
	var wouldFree, pending, deliverables int64
	for _, cat := range r.Categories {
		wouldFree += cat.WouldFreeBytes
		pending += cat.PendingBytes
		if cat.Category == "artifacts" {
			deliverables += cat.PendingBytes
		}
	}
	headline := "freed " + humanBytes(r.FreedBytes)
	if r.DryRun {
		headline = "would free " + humanBytes(wouldFree)
	}
	if pending > 0 {
		headline += fmt.Sprintf(", %s awaiting confirmation", humanBytes(pending))
		if deliverables > 0 {
			headline += fmt.Sprintf(" (%s of artifacts: --confirm-artifacts)", humanBytes(deliverables))
		}
	}
	if _, err := fmt.Fprintf(w, "Cleanup (%s): %s\n", kind, headline); err != nil {
		return err
	}
	for _, cat := range r.Categories {
		line := fmt.Sprintf("  %s: ", cat.Category)
		switch {
		case cat.Skipped != "":
			line += "skipped (" + cat.Skipped + ")"
		case r.DryRun:
			line += fmt.Sprintf("would free %s (%d), %d awaiting confirmation, %d kept", humanBytes(cat.WouldFreeBytes), cat.WouldFree, cat.Pending, cat.Kept)
		default:
			line += fmt.Sprintf("freed %s (%d), %d awaiting confirmation, %d kept, %d failed", humanBytes(cat.FreedBytes), cat.Freed, cat.Pending, cat.Kept, cat.Failed)
		}
		if cat.Error != "" {
			line += " — error: " + cat.Error
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
		for _, item := range cat.Items {
			label := item.What
			switch {
			case item.SessionID != "":
				label = item.SessionID + " " + label
			case item.ProjectID != "":
				label = item.ProjectID + " " + label
			case item.Path != "":
				label += " " + filepath.Base(item.Path)
			}
			text := fmt.Sprintf("    %-18s %10s  %s", cleanupStatusLabel[item.Status], humanBytes(item.Bytes), label)
			if item.Reason != "" {
				text += " (" + item.Reason + ")"
			}
			if _, err := fmt.Fprintln(w, text); err != nil {
				return err
			}
		}
		if cat.ItemsTruncated {
			if _, err := fmt.Fprintln(w, "    …"); err != nil {
				return err
			}
		}
	}
	if r.Error != "" {
		if _, err := fmt.Fprintf(w, "Run ended early: %s\n", r.Error); err != nil {
			return err
		}
	}
	return nil
}

func newCleanupStatusCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the cleanup policy, the last run and when the next passes are due",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var status apitypes.CleanupStatusResponse
			if err := ctx.getJSON(cmd.Context(), "cleanup", &status); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, status)
			}
			if err := printCleanupPolicy(out, status.Policy); err != nil {
				return err
			}
			if status.PolicyError != "" {
				if _, err := fmt.Fprintf(out, "The stored policy is invalid (%s); automatic cleanup is off until a policy is saved.\n", status.PolicyError); err != nil {
					return err
				}
			}
			if status.FirstPassPending && status.Policy.Automatic {
				if _, err := fmt.Fprintln(out, "The first scheduled pass is a dry run: it only reports what later passes will remove."); err != nil {
					return err
				}
			}
			if status.Running {
				if _, err := fmt.Fprintln(out, "A cleanup run is in progress."); err != nil {
					return err
				}
			}
			if status.NextLightRunAt != "" {
				if _, err := fmt.Fprintf(out, "Next light pass: %s, next full pass: %s\n", status.NextLightRunAt, status.NextFullRunAt); err != nil {
					return err
				}
			}
			if status.LastRun != nil {
				if _, err := fmt.Fprintf(out, "\nLast run (%s, %s):\n", status.LastRun.StartedAt, status.LastRun.Trigger); err != nil {
					return err
				}
				if err := printCleanupReport(out, *status.LastRun); err != nil {
					return err
				}
				if err := printKeptWorktrees(out, *status.LastRun); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintln(out, "\nNo cleanup has run yet."); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newCleanupHistoryCommand(ctx *commandContext) *cobra.Command {
	var (
		limit      int
		includeDry bool
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "history",
		Short: "List recent cleanup runs and what each freed",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 200 {
				return usageError{errors.New("--limit must be 1-200")}
			}
			params := url.Values{}
			params.Set("limit", strconv.Itoa(limit))
			if includeDry {
				params.Set("includeDryRuns", "true")
			}
			var res struct {
				Runs []apitypes.CleanupRunReport `json:"runs"`
			}
			if err := ctx.getJSON(cmd.Context(), apiPath("cleanup/runs", params), &res); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, res)
			}
			if len(res.Runs) == 0 {
				_, err := fmt.Fprintln(out, "No cleanup runs yet.")
				return err
			}
			for _, run := range res.Runs {
				kind := run.Pass
				if run.DryRun {
					kind += " (dry run)"
				}
				line := fmt.Sprintf("%s  %-17s %-8s freed %s", run.StartedAt, kind, run.Trigger, humanBytes(run.FreedBytes))
				if run.Error != "" {
					line += "  error: " + run.Error
				}
				if _, err := fmt.Fprintln(out, line); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "Number of runs to show (1-200)")
	cmd.Flags().BoolVar(&includeDry, "include-dry-runs", false, "Include dry runs")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newCleanupPolicyCommand(ctx *commandContext) *cobra.Command {
	var (
		sets   []string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Show or change the cleanup policy",
		Long: "Show the cleanup policy, or change it with --set key=value (repeatable). Keys: automatic, " +
			"worktrees.enabled, worktrees.retentionDays, worktrees.requireMerged, artifacts.enabled, artifacts.retentionDays, artifacts.autoPrune, " +
			"artifacts.deliverableAction (archive|delete), artifacts.autoDeliverables, sessionTemp.enabled, harnessCaches.enabled, " +
			"harnessCaches.retentionDays, systemTemp.enabled, systemTemp.minAgeDays, goBuildCache.enabled, containers.enabled, containers.volumeRetentionDays, " +
			"branches.enabled, branches.mergedRetentionDays, branches.staleRetentionDays, branches.deleteRemote. Set systemTemp.patterns with the API or Settings.",
		Example: `  tallpine cleanup policy
  tallpine cleanup policy --set artifacts.retentionDays=14 --set worktrees.retentionDays=0`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var policy apitypes.CleanupPolicy
			if len(sets) == 0 {
				var status apitypes.CleanupStatusResponse
				if err := ctx.getJSON(cmd.Context(), "cleanup", &status); err != nil {
					return err
				}
				policy = status.Policy
			} else {
				patch, err := cleanupPolicyPatch(sets)
				if err != nil {
					return err
				}
				if err := ctx.patchJSON(cmd.Context(), "cleanup/policy", patch, &policy); err != nil {
					return err
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), policy)
			}
			return printCleanupPolicy(cmd.OutOrStdout(), policy)
		},
	}
	cmd.Flags().StringArrayVar(&sets, "set", nil, "Change one setting, key=value (repeatable)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

// cleanupPolicyPatch turns dotted key=value pairs into a nested JSON patch.
// The daemon validates keys and ranges; this only shapes the document.
func cleanupPolicyPatch(sets []string) (map[string]any, error) {
	patch := map[string]any{}
	for _, set := range sets {
		key, raw, ok := strings.Cut(set, "=")
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if !ok || key == "" || raw == "" {
			return nil, usageError{fmt.Errorf("--set %q: want key=value", set)}
		}
		var value any = raw
		if n, err := strconv.Atoi(raw); err == nil {
			value = n
		} else if raw == "true" || raw == "false" {
			value = raw == "true"
		}
		parts := strings.Split(key, ".")
		node := patch
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				node[part] = child
			}
			node = child
		}
		node[parts[len(parts)-1]] = value
	}
	return patch, nil
}

func printCleanupPolicy(w io.Writer, p apitypes.CleanupPolicy) error {
	onOff := func(v bool) string {
		if v {
			return "on"
		}
		return "off"
	}
	deliverables := "after confirmation"
	if p.Artifacts.AutoDeliverables {
		deliverables = "automatically"
	}
	worktrees := "never with uncommitted changes"
	if p.Worktrees.RequireMerged {
		worktrees = "only when merged or pushed, never with uncommitted changes"
	}
	worktrees += "; gitignored files in them (such as .env) go too"
	history := "after confirmation"
	if p.Artifacts.AutoPrune {
		history = "automatically"
	}
	_, err := fmt.Fprintf(w, "Automatic cleanup: %s (light pass hourly, full pass daily)\n"+
		"  Worktrees: %s, removed %d day(s) after a session ends, %s\n"+
		"  Artifacts: %s, after %d day(s): history and tool dirs removed %s, deliverables %sd %s\n"+
		"  Session temp dirs: %s, removed when their session has ended\n"+
		"  Harness caches: %s, removed after %d day(s) without a live session in the project\n"+
		"  System temp leftovers: %s (%s, untouched for %d day(s))\n"+
		"  Go build cache: %s (entries unused for 2 days)\n"+
		"  Docker: %s, containers of ended sessions removed, volumes after %d day(s); tallpine.spare=true is kept\n"+
		"  Branches: %s, merged after %d day(s), pushed-but-unmerged after %d day(s), remote branches of merged PRs %s\n",
		onOff(p.Automatic),
		onOff(p.Worktrees.Enabled), p.Worktrees.RetentionDays, worktrees,
		onOff(p.Artifacts.Enabled), p.Artifacts.RetentionDays, history,
		p.Artifacts.DeliverableAction, deliverables,
		onOff(p.SessionTemp.Enabled),
		onOff(p.HarnessCaches.Enabled), p.HarnessCaches.RetentionDays,
		map[bool]string{true: "on", false: "off, reported only"}[p.SystemTemp.Enabled], strings.Join(p.SystemTemp.Patterns, ", "), p.SystemTemp.MinAgeDays,
		map[bool]string{true: "on", false: "off, reported only"}[p.GoBuildCache.Enabled],
		onOff(p.Containers.Enabled), p.Containers.VolumeRetentionDays,
		map[bool]string{true: "on", false: "off, reported only"}[p.Branches.Enabled], p.Branches.MergedRetentionDays, p.Branches.StaleRetentionDays,
		map[bool]string{true: "deleted", false: "kept"}[p.Branches.DeleteRemote])
	return err
}
