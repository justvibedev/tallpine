package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func (c *commandContext) killSession(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	if opts.project != "" {
		if _, err := c.fetchScopedSession(ctx, id, opts.project); err != nil {
			return err
		}
	}
	var res apitypes.KillSessionResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/kill", struct{}{}, &res); err != nil {
		return err
	}
	if res.CleanupPending {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "session %s killed (workspace cleanup pending)\n", res.SessionID)
		return err
	}
	if res.Freed {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "session %s killed\n", res.SessionID)
		return err
	}
	// freed=false: the workspace was preserved (e.g. uncommitted changes) — the
	// session is terminated either way, but the worktree is left for inspection.
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "session %s killed (workspace preserved)\n", res.SessionID)
	return err
}

func (c *commandContext) restoreSession(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	sess, err := c.fetchScopedSession(ctx, id, opts.project)
	if err != nil {
		return err
	}
	if !sess.IsTerminated && sess.Activity.State == "exited" {
		return c.resumeSessionAgent(ctx, cmd, id, sessionOptions{})
	}
	var res apitypes.RestoreSessionResponse
	if err := c.postSetupJSON(ctx, "sessions/"+url.PathEscape(id)+"/restore", struct{}{}, &res); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "session %s restored\n", res.SessionID); err != nil {
		return err
	}
	if res.Session.ProjectID != "" {
		if _, err := fmt.Fprintf(out, "  project: %s\n", res.Session.ProjectID); err != nil {
			return err
		}
	}
	return nil
}

func (c *commandContext) exitSessionAgent(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	if opts.project != "" {
		if _, err := c.fetchScopedSession(ctx, id, opts.project); err != nil {
			return err
		}
	}
	var res apitypes.ExitAgentResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/exit-agent", struct{}{}, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "agent exited for session %s\n", res.SessionID)
	return err
}

func (c *commandContext) resumeSessionAgent(ctx context.Context, cmd *cobra.Command, id string, opts sessionOptions) error {
	if opts.project != "" {
		if _, err := c.fetchScopedSession(ctx, id, opts.project); err != nil {
			return err
		}
	}
	var res apitypes.ResumeAgentResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/resume-agent", struct{}{}, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "agent resumed for session %s\n", res.SessionID); err != nil {
		return err
	}
	if res.ResumeMode != "" {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "  mode: %s\n", res.ResumeMode)
		return err
	}
	return nil
}

func (c *commandContext) renameSession(ctx context.Context, cmd *cobra.Command, id, displayName string, opts sessionOptions) error {
	if opts.project != "" {
		if _, err := c.fetchScopedSession(ctx, id, opts.project); err != nil {
			return err
		}
	}
	name := strings.TrimSpace(displayName)
	var res apitypes.RenameSessionResponse
	if err := c.patchJSON(ctx, "sessions/"+url.PathEscape(id), apitypes.RenameSessionRequest{DisplayName: name}, &res); err != nil {
		return err
	}
	sessionID := res.SessionID
	if sessionID == "" {
		sessionID = domain.SessionID(id)
	}
	if res.DisplayName != "" {
		name = res.DisplayName
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "session %s renamed to %q\n", sessionID, name)
	return err
}
