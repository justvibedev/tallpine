package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

type orchestratorNotifyOptions struct {
	project string
	target  string
	message string
}

func newOrchestratorNotifyCommand(ctx *commandContext) *cobra.Command {
	var opts orchestratorNotifyOptions
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Send one message to a project's live orchestrator",
		Long: `Sends one message to the project's live orchestrator: the --target session when it
is live, else the oldest live orchestrator of the project. Exits non-zero when
the project has no live orchestrator or the message could not be delivered.

The command acts as the machine, never as an agent: TALLPINE_SESSION_ID,
TALLPINE_SESSION_TOKEN and TALLPINE_PROJECT_ID are ignored, so the message
carries no "[from <session>]" prefix. Used by the local CI loop and release
builds to report to the main-health orchestrator.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.notifyOrchestrator(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.project, "project", "", "Project id (required)")
	cmd.Flags().StringVar(&opts.target, "target", "", "Preferred orchestrator session id, used when it is live")
	cmd.Flags().StringVar(&opts.message, "message", "", "Message body (required)")
	return cmd
}

// pickNotifyOrchestrator returns the live orchestrator to notify: target when
// it is live in the project, else the oldest live one (ties broken by id).
func pickNotifyOrchestrator(sessions []apitypes.SessionView, project, target string) (id, why string, ok bool) {
	var live []apitypes.SessionView
	for _, s := range sessions {
		if s.Kind == "orchestrator" && string(s.ProjectID) == project && !s.IsTerminated {
			live = append(live, s)
		}
	}
	if len(live) == 0 {
		return "", "", false
	}
	for _, s := range live {
		if target != "" && string(s.ID) == target {
			return string(s.ID), "target names a live session", true
		}
	}
	sort.Slice(live, func(i, j int) bool {
		if !live[i].CreatedAt.Equal(live[j].CreatedAt) {
			return live[i].CreatedAt.Before(live[j].CreatedAt)
		}
		return live[i].ID < live[j].ID
	})
	why = "no target; using the oldest live orchestrator"
	if target != "" {
		why = "target " + target + " is not live; using the oldest live orchestrator"
	}
	return string(live[0].ID), why, true
}

func (c *commandContext) notifyOrchestrator(ctx context.Context, opts orchestratorNotifyOptions) error {
	project := strings.TrimSpace(opts.project)
	if project == "" {
		return usageError{errors.New("usage: --project is required")}
	}
	if strings.TrimSpace(opts.message) == "" {
		return usageError{errors.New("usage: --message is required")}
	}
	// Act as the machine: an agent's identity would stamp "[from <id>]" on the
	// message and send the agent's scoped token.
	for _, name := range []string{brand.Env("SESSION_ID"), brand.Env("PROJECT_ID"), sessiontoken.EnvVar, sessiontoken.RunFileEnvVar, sessiontoken.AltEnvVar, sessiontoken.AltRunFileEnvVar} {
		_ = os.Unsetenv(name)
	}
	var res apitypes.ListSessionsResponse
	if err := c.getJSON(ctx, "orchestrators", &res); err != nil {
		return err
	}
	id, why, ok := pickNotifyOrchestrator(res.Sessions, project, strings.TrimSpace(opts.target))
	if !ok {
		return fmt.Errorf("no live orchestrator for project %q", project)
	}
	if _, err := fmt.Fprintf(c.deps.Out, "notify target %s (%s)\n", id, why); err != nil {
		return err
	}
	return c.sendMessage(ctx, sendOptions{session: id, message: opts.message})
}
