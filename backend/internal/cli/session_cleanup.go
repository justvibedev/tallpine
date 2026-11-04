package cli

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func (c *commandContext) cleanupSessions(ctx context.Context, cmd *cobra.Command, opts sessionCleanupOptions) error {
	candidates, err := c.previewCleanupSessions(ctx, opts.project)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintln(out, "Checking for completed sessions..."); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	if len(candidates) == 0 {
		_, err := fmt.Fprintln(out, "  No sessions to clean up.")
		return err
	}
	labels := cleanupLabels(candidates, opts.project)
	for _, label := range labels {
		if _, err := fmt.Fprintf(out, "  Would clean %s\n", label); err != nil {
			return err
		}
	}
	if opts.dryRun {
		_, err = fmt.Fprintln(out, "\n(dry-run: no sessions were removed)")
		return err
	}
	if !opts.yes {
		confirmed, err := confirmSessionCleanup(cmd, len(candidates), opts.project)
		if err != nil {
			return err
		}
		if !confirmed {
			_, err := fmt.Fprintln(out, "aborted")
			return err
		}
	}
	params := url.Values{}
	if opts.project != "" {
		params.Set("project", opts.project)
	}
	var res apitypes.CleanupSessionsResponse
	if err := c.postJSON(ctx, apiPath("sessions/cleanup", params), struct{}{}, &res); err != nil {
		return err
	}
	cleaned := res.Cleaned
	labelByID := cleanupLabelByID(candidates, opts.project)
	for _, id := range cleaned {
		label := id
		if mapped := labelByID[string(id)]; mapped != "" {
			label = domain.SessionID(mapped)
		}
		if _, err := fmt.Fprintf(out, "  Cleaned: %s\n", label); err != nil {
			return err
		}
	}
	for _, id := range res.AlreadyGone {
		label := id
		if mapped := labelByID[string(id)]; mapped != "" {
			label = domain.SessionID(mapped)
		}
		if _, err := fmt.Fprintf(out, "  Already gone: %s (workspace was missing; nothing reclaimed)\n", label); err != nil {
			return err
		}
	}
	for _, skip := range res.Skipped {
		label := skip.SessionID
		if mapped := labelByID[string(skip.SessionID)]; mapped != "" {
			label = domain.SessionID(mapped)
		}
		if _, err := fmt.Fprintf(out, "  Skipped: %s (%s)\n", label, skip.Reason); err != nil {
			return err
		}
	}
	summary := fmt.Sprintf("\nCleanup complete. %d session%s cleaned", len(cleaned), pluralS(len(cleaned)))
	if len(res.AlreadyGone) > 0 {
		summary += fmt.Sprintf(", %d already gone", len(res.AlreadyGone))
	}
	if len(res.Skipped) > 0 {
		summary += fmt.Sprintf(", %d skipped", len(res.Skipped))
	}
	_, err = fmt.Fprintf(out, "%s.\n", summary)
	return err
}

func (c *commandContext) previewCleanupSessions(ctx context.Context, project string) ([]apitypes.SessionView, error) {
	params := url.Values{}
	params.Set("active", "false")
	if project != "" {
		params.Set("project", project)
	}
	var res apitypes.ListSessionsResponse
	if err := c.getJSON(ctx, apiPath("sessions", params), &res); err != nil {
		return nil, err
	}
	return filterAndSortSessions(res.Sessions, true), nil
}

func cleanupLabels(sessions []apitypes.SessionView, scopedProject string) []string {
	labels := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		labels = append(labels, cleanupLabel(sess, scopedProject))
	}
	return labels
}

func cleanupLabelByID(sessions []apitypes.SessionView, scopedProject string) map[string]string {
	labels := make(map[string]string, len(sessions))
	for _, sess := range sessions {
		labels[string(sess.ID)] = cleanupLabel(sess, scopedProject)
	}
	return labels
}

func cleanupLabel(sess apitypes.SessionView, scopedProject string) string {
	if scopedProject == "" && sess.ProjectID != "" {
		return string(sess.ProjectID) + ":" + string(sess.ID)
	}
	return string(sess.ID)
}

func confirmSessionCleanup(cmd *cobra.Command, count int, project string) (bool, error) {
	scope := " across all projects"
	if project != "" {
		scope = fmt.Sprintf(" in project %q", project)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Clean %d terminated session%s%s? Type yes to confirm: ", count, pluralS(count), scope); err != nil {
		return false, err
	}
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(line), "yes"), nil
}
