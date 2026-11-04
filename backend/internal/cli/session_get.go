package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func (c *commandContext) getSession(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	res, err := c.fetchScopedSessionResponse(ctx, id, opts.project)
	if err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	if err := writeSessionDetails(cmd, res.Session); err != nil {
		return err
	}
	return writeLastAssistantMessage(cmd, res.LastAssistantMessage)
}

// writeLastAssistantMessage prints the latest turn's final message, indented
// under its timestamp.
func writeLastAssistantMessage(cmd *cobra.Command, last *apitypes.SessionLastAssistantMessage) error {
	if last == nil || strings.TrimSpace(last.Text) == "" {
		return nil
	}
	text := "  " + strings.ReplaceAll(strings.TrimSpace(last.Text), "\n", "\n  ")
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "last message (%s):\n%s\n", last.At.Format(time.RFC3339), text)
	return err
}

func (c *commandContext) fetchScopedSession(ctx context.Context, id, project string) (apitypes.SessionView, error) {
	res, err := c.fetchScopedSessionResponse(ctx, id, project)
	return res.Session, err
}

func (c *commandContext) fetchScopedSessionResponse(ctx context.Context, id, project string) (apitypes.SessionResponse, error) {
	var res apitypes.SessionResponse
	if err := c.getJSON(ctx, "sessions/"+url.PathEscape(id), &res); err != nil {
		return apitypes.SessionResponse{}, err
	}
	if project != "" && string(res.Session.ProjectID) != project {
		return apitypes.SessionResponse{}, usageError{fmt.Errorf("session %s is not in project %s", id, project)}
	}
	return res, nil
}

func writeSessionDetails(cmd *cobra.Command, sess apitypes.SessionView) error {
	out := cmd.OutOrStdout()
	fields := [][2]string{
		{"id", string(sess.ID)},
		{"project", string(sess.ProjectID)},
		{"name", sess.DisplayName},
		{"role", sessionRole(sess)},
		{"status", string(sess.Status)},
		{"activity", string(sess.Activity.State)},
		{"harness", string(sess.Harness)},
		{"model", sess.Model},
		{"issue", string(sess.IssueID)},
		{"branch", formatBranchState(sess)},
		{"terminated", fmt.Sprintf("%t", sess.IsTerminated)},
	}
	if sess.PausedUntil != nil {
		fields = append(fields, [2]string{"paused until", sess.PausedUntil.Format(time.RFC3339) + " (provider usage limit)"})
	}
	for _, field := range fields {
		if field[1] == "" {
			continue
		}
		if _, err := fmt.Fprintf(out, "%s: %s\n", field[0], field[1]); err != nil {
			return err
		}
	}
	if !sess.CreatedAt.IsZero() {
		if _, err := fmt.Fprintf(out, "created: %s\n", sess.CreatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if !sess.UpdatedAt.IsZero() {
		if _, err := fmt.Fprintf(out, "updated: %s\n", sess.UpdatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return nil
}

// formatBranchState renders the daemon's observed branch facts, for example
// "feat/x (3 commits, 1 not pushed to origin/feat/x)".
func formatBranchState(sess apitypes.SessionView) string {
	state := sess.BranchState
	if sess.Branch == "" || state == nil {
		return sess.Branch
	}
	commits := fmt.Sprintf("%d commit", state.Commits)
	if state.Commits != 1 {
		commits += "s"
	}
	switch {
	case state.RemoteBranch == "":
		if state.Commits == 0 {
			return fmt.Sprintf("%s (%s)", sess.Branch, commits)
		}
		return fmt.Sprintf("%s (%s, not pushed)", sess.Branch, commits)
	case state.Unpushed > 0:
		return fmt.Sprintf("%s (%s, %d not pushed to %s)", sess.Branch, commits, state.Unpushed, state.RemoteBranch)
	default:
		return fmt.Sprintf("%s (%s, pushed to %s)", sess.Branch, commits, state.RemoteBranch)
	}
}

func sessionRole(sess apitypes.SessionView) string {
	if sess.Kind == "orchestrator" {
		return "orchestrator"
	}
	return "worker"
}

func formatSessionAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
