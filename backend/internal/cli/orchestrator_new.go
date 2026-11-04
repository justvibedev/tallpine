package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type orchestratorNewBody struct {
	ProjectID string `json:"projectId"`
	Lane      string `json:"lane,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	Model     string `json:"model,omitempty"`
}

type orchestratorNewResponse struct {
	orchestratorHandoffResponse
	PromptDelivered bool `json:"promptDelivered,omitempty"`
}

func newOrchestratorNewCommand(ctx *commandContext) *cobra.Command {
	var project, lane, prompt, model string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Start an orchestrator, optionally bound to a lane",
		Long: "Starts the project's orchestrator, or returns the running one. With --lane <ref> it starts " +
			"(or returns) the orchestrator that owns that top-level backlog item: workers it spawns join the " +
			"lane and their reports come to this orchestrator only. Without --lane it is the project's " +
			"default orchestrator, which also receives work that has no lane.\n\n" +
			"With --prompt <text> the new orchestrator's first message is its task brief, delivered once it is ready; for an already-running one it is sent as a normal message. An " +
			"orchestrator can start only projects its own project created, and only while its project's " +
			"crossProjectOrchestration agent policy is on (Settings > Agent policy; off by default).",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := ctx.resolveSpawnProject(cmd.Context(), project)
			if err != nil {
				return err
			}
			var res orchestratorNewResponse
			// The prompt rides the start: the daemon delivers it as the first
			// turn once the session's chat controller is live, so there is no
			// race with a session that is still starting.
			body := orchestratorNewBody{ProjectID: resolved.ID, Lane: strings.TrimSpace(lane), Prompt: strings.TrimSpace(prompt), Model: strings.TrimSpace(model)}
			// No client timeout: a first start runs the project's Setup.
			if err := ctx.postSetupJSON(cmd.Context(), "orchestrators", body, &res); err != nil {
				return err
			}
			if err := ctx.awaitOrchestratorStart(cmd.Context(), res.Orchestrator.ID); err != nil {
				return err
			}
			if body.Prompt != "" && !res.PromptDelivered {
				// A running orchestrator was returned: a later message, sent as one.
				if err := ctx.sendMessage(cmd.Context(), sendOptions{session: res.Orchestrator.ID, message: prompt}); err != nil {
					return err
				}
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			where := "the default orchestrator"
			if body.Lane != "" {
				where = "the orchestrator for lane " + body.Lane
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s is %s\n", res.Orchestrator.ID, where)
			return err
		},
	}
	addBacklogProjectFlag(cmd, &project, &jsonOutput)
	cmd.Flags().StringVar(&prompt, "prompt", "", "First message to send to the orchestrator after it starts")
	cmd.Flags().StringVar(&model, "model", "", "Model for a new lane orchestrator (with --lane); default is the project's")
	cmd.Flags().StringVar(&lane, "lane", "", "Top-level backlog item ref (e.g. B12) this orchestrator owns")
	return cmd
}

// orchestratorStartWait bounds how long `orchestrator new` waits for the
// asynchronous start to settle before reporting success.
var (
	orchestratorStartWait = 90 * time.Second
	orchestratorStartPoll = 500 * time.Millisecond
)

// awaitOrchestratorStart polls the session until its background start is no
// longer provisioning, so a failed start (e.g. an unresolved default branch)
// is reported to the caller instead of printing "is the default orchestrator".
// A start still running at the deadline is not an error.
func (c *commandContext) awaitOrchestratorStart(ctx context.Context, id string) error {
	deadline := time.Now().Add(orchestratorStartWait)
	for {
		res, err := c.fetchScopedSessionResponse(ctx, id, "")
		if err != nil {
			return nil //nolint:nilerr // the start was accepted; a read failure is not a start failure
		}
		switch res.Session.ProvisionState {
		case "failed":
			msg := res.Session.ProvisionError
			if msg == "" {
				msg = "unknown error"
			}
			return fmt.Errorf("orchestrator %s failed to start: %s", id, msg)
		case "provisioning":
		default:
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(orchestratorStartPoll):
		}
	}
}
