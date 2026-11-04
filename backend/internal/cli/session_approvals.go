package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// newSessionApprovalsCommand lists a worker's pending permission prompts with
// the exact request, so an orchestrator can decide without opening the session.
// The daemon refuses it unless the project lets orchestrators answer worker
// prompts.
func newSessionApprovalsCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "approvals <session>",
		Short: "List a worker's pending permission prompts with the exact command or tool input",
		Args:  oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			var res apitypes.PendingApprovalsResponse
			if err := ctx.getJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/conversation/approvals", &res); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				if res.Approvals == nil {
					res.Approvals = []apitypes.PendingApproval{}
				}
				return writeJSON(out, res)
			}
			if len(res.Approvals) == 0 && len(res.Inputs) == 0 {
				_, err := fmt.Fprintf(out, "no pending approvals for session %s\n", id)
				return err
			}
			for _, in := range res.Inputs {
				if _, err := fmt.Fprintf(out, "question %s\n  %s\n  detail: %s\n  answer with: %s session answer %s %s --action accept|decline|cancel [--content '{...}']\n",
					in.RequestID, in.Summary, strings.TrimSpace(string(in.Detail)), brand.CLIName, id, in.RequestID); err != nil {
					return err
				}
			}
			for _, a := range res.Approvals {
				if _, err := fmt.Fprintf(out, "request %s\n  %s\n  detail: %s\n", a.RequestID, a.Summary, strings.TrimSpace(string(a.Detail))); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

// newSessionResolveApprovalCommand answers one pending prompt. approve picks the
// request's one-time allow and deny its one-time reject: a standing allow is
// for the user, and the daemon refuses it for an agent either way.
func newSessionResolveApprovalCommand(ctx *commandContext, verb, wantKind string) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   verb + " <session> <requestId>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a worker's pending permission prompt (orchestrator, when the project allows it)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			requestID := strings.TrimSpace(args[1])
			if requestID == "" {
				return usageError{errors.New("request id is required")}
			}
			var list apitypes.PendingApprovalsResponse
			if err := ctx.getJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/conversation/approvals", &list); err != nil {
				return err
			}
			decisionID := ""
			for _, a := range list.Approvals {
				if a.RequestID != requestID {
					continue
				}
				for _, d := range a.Decisions {
					if d.Kind == wantKind {
						decisionID = d.ID
						break
					}
				}
			}
			if decisionID == "" {
				return fmt.Errorf("session %s has no pending approval %s offering a one-time %s", id, requestID, verb)
			}
			body := map[string]string{"decisionId": decisionID}
			if note = strings.TrimSpace(note); note != "" {
				body["note"] = note
			}
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/conversation/approvals/"+url.PathEscape(requestID)+"/resolve", body, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "answered request %s for session %s (%s)\n", requestID, id, verb)
			return err
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Short reason recorded in the audit trail")
	return cmd
}

// newSessionAnswerCommand answers a worker's in-chat question (a form or URL
// request listed by `session approvals`) with accept, decline or cancel.
func newSessionAnswerCommand(ctx *commandContext) *cobra.Command {
	var action, content string
	cmd := &cobra.Command{
		Use:   "answer <session> <requestId>",
		Short: "Answer a worker's pending in-chat question (orchestrator, when the project allows it)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			requestID := strings.TrimSpace(args[1])
			if requestID == "" {
				return usageError{errors.New("request id is required")}
			}
			switch action {
			case "accept", "decline", "cancel":
			default:
				return usageError{errors.New("--action must be accept, decline, or cancel")}
			}
			body := map[string]any{"action": action}
			if strings.TrimSpace(content) != "" {
				var parsed map[string]any
				if err := json.Unmarshal([]byte(content), &parsed); err != nil {
					return usageError{fmt.Errorf("--content must be a JSON object: %w", err)}
				}
				body["content"] = parsed
			}
			if err := ctx.postJSON(cmd.Context(), "sessions/"+url.PathEscape(id)+"/conversation/inputs/"+url.PathEscape(requestID)+"/resolve", body, nil); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "answered question %s for session %s (%s)\n", requestID, id, action)
			return err
		},
	}
	cmd.Flags().StringVar(&action, "action", "", "accept, decline, or cancel (required)")
	cmd.Flags().StringVar(&content, "content", "", "JSON object with the form answers, only with accept")
	return cmd
}
