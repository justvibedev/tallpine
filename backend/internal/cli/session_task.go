package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func newSessionTaskCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage a session agent's background tasks",
	}
	cmd.AddCommand(newSessionTaskStopCommand(ctx))
	return cmd
}

func newSessionTaskStopCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <session> <taskId>",
		Short: "Stop one background task of a session's agent",
		Long: "Stop a background task (a background command or monitor) the session's agent is running. " +
			"A worker may stop its own tasks; an orchestrator may also stop those of its project's workers. " +
			"The task ids are in `tallpine session get <session> --json` under backgroundTasks.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			taskID := strings.TrimSpace(args[1])
			if taskID == "" {
				return usageError{errors.New("task id is required")}
			}
			body := map[string]string{"taskId": taskID}
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/background-tasks/stop", body, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "stopped task %s of session %s\n", taskID, id)
			return err
		},
	}
}
