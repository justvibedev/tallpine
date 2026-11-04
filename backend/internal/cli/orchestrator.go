package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type orchestratorListOptions struct {
	json bool
}

type orchestratorListOutput struct {
	Data []sessionListEntry `json:"data"`
}

func newOrchestratorCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orchestrator",
		Short: "Manage orchestrator sessions",
	}
	cmd.AddCommand(newOrchestratorListCommand(ctx))
	cmd.AddCommand(newOrchestratorNewCommand(ctx))
	cmd.AddCommand(newOrchestratorHandoffCommand(ctx))
	cmd.AddCommand(newOrchestratorEndCommand(ctx))
	cmd.AddCommand(newOrchestratorNotifyCommand(ctx))
	cmd.AddCommand(newOrchestratorIntentCommand(ctx))
	cmd.AddCommand(newOrchestratorResearchCommand(ctx))
	return cmd
}

func newOrchestratorListCommand(ctx *commandContext) *cobra.Command {
	var opts orchestratorListOptions
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List orchestrator sessions",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.listOrchestrators(cmd.Context(), cmd, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) listOrchestrators(ctx context.Context, cmd *cobra.Command, opts orchestratorListOptions) error {
	var res apitypes.ListSessionsResponse
	if err := c.getJSON(ctx, "orchestrators", &res); err != nil {
		return err
	}
	orchestrators := filterAndSortOrchestrators(res.Sessions)
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), orchestratorListOutput{Data: sessionListEntries(orchestrators, nil)})
	}
	return writeOrchestratorList(cmd, orchestrators)
}

func filterAndSortOrchestrators(sessions []apitypes.SessionView) []apitypes.SessionView {
	out := make([]apitypes.SessionView, 0, len(sessions))
	for _, sess := range sessions {
		if sess.Kind != "orchestrator" {
			continue
		}
		out = append(out, sess)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectID != out[j].ProjectID {
			return out[i].ProjectID < out[j].ProjectID
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func writeOrchestratorList(cmd *cobra.Command, sessions []apitypes.SessionView) error {
	out := cmd.OutOrStdout()
	if len(sessions) == 0 {
		_, err := fmt.Fprintln(out, "(no orchestrators)")
		return err
	}
	currentProject := ""
	for _, sess := range sessions {
		if string(sess.ProjectID) != currentProject {
			if currentProject != "" {
				if _, err := fmt.Fprintln(out); err != nil {
					return err
				}
			}
			currentProject = string(sess.ProjectID)
			if _, err := fmt.Fprintf(out, "%s:\n", currentProject); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(out, "  %s", sess.ID); err != nil {
			return err
		}
		parts := orchestratorLineParts(sess)
		if len(parts) > 0 {
			if _, err := fmt.Fprintf(out, "  %s", strings.Join(parts, "  ")); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	return nil
}

func orchestratorLineParts(sess apitypes.SessionView) []string {
	parts := []string{}
	if !sess.Activity.LastActivityAt.IsZero() {
		parts = append(parts, "("+formatSessionAge(time.Since(sess.Activity.LastActivityAt))+")")
	}
	if sess.Status != "" {
		parts = append(parts, "["+string(sess.Status)+"]")
	}
	if sess.IsTerminated {
		parts = append(parts, "terminated")
	}
	return parts
}

type orchestratorHandoffOptions struct {
	note    string
	session string
	json    bool
}

type orchestratorHandoffResponse struct {
	Orchestrator struct {
		ID        string `json:"id"`
		ProjectID string `json:"projectId"`
	} `json:"orchestrator"`
}

func newOrchestratorHandoffCommand(ctx *commandContext) *cobra.Command {
	var opts orchestratorHandoffOptions
	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Replace this orchestrator with a fresh one that continues from a state note",
		Long: "Starts a fresh orchestrator for the project, seeded to read --note and continue, " +
			"then retires this one (its history stays readable). Workers keep running and report " +
			"to the new orchestrator. The note must be a file in this orchestrator's artifact directory.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.handoffOrchestrator(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.note, "note", "", "State note file in this orchestrator's artifact directory (default: its newest note)")
	cmd.Flags().StringVar(&opts.session, "session", "", "Orchestrator session id (default: $TALLPINE_SESSION_ID)")
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) handoffOrchestrator(cmd *cobra.Command, opts orchestratorHandoffOptions) error {
	id := strings.TrimSpace(opts.session)
	if id == "" {
		id = strings.TrimSpace(brand.Getenv("SESSION_ID"))
	}
	if id == "" || !sessionIDPattern.MatchString(id) {
		return usageError{errors.New("usage: run inside an orchestrator session (TALLPINE_SESSION_ID) or pass --session")}
	}
	body := struct {
		NotePath string `json:"notePath,omitempty"`
	}{}
	if note := strings.TrimSpace(opts.note); note != "" {
		abs, err := filepath.Abs(note)
		if err != nil {
			return usageError{fmt.Errorf("usage: --note: %w", err)}
		}
		body.NotePath = abs
	}
	var res orchestratorHandoffResponse
	// No client timeout: the daemon retires this orchestrator, which can take
	// longer than the default, and hanging up would cancel nothing it needs.
	if err := c.postSetupJSON(cmd.Context(), "orchestrators/"+url.PathEscape(id)+"/handoff", body, &res); err != nil {
		return err
	}
	if opts.json {
		return writeJSON(cmd.OutOrStdout(), res)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "Handed off to new orchestrator %s; %s is retired. Workers keep running.\n", res.Orchestrator.ID, id)
	return err
}
