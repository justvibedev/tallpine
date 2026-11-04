package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type reportOptions struct {
	note                                string
	noteSet                             bool
	artifacts, prsCreated, prsReviewed  []string
	checkpoint, needsInput, stuck, done bool
	defers                              []string
}

type reportAPIResponse struct {
	ID       string `json:"id"`
	Notice   string `json:"notice"`
	Deferred []struct {
		Line   string `json:"line"`
		Status string `json:"status"`
		Ref    string `json:"ref"`
		Reason string `json:"reason"`
	} `json:"deferred"`
}

func newReportCommand(ctx *commandContext) *cobra.Command {
	var o reportOptions
	cmd := &cobra.Command{Use: "report <free-form text>", Short: "Report worker progress to the orchestrator", Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		o.noteSet = cmd.Flags().Changed("note")
		return ctx.report(cmd.Context(), cmd.OutOrStdout(), args, o)
	}}
	f := cmd.Flags()
	f.StringVar(&o.note, "note", "", "Structured state note")
	f.StringArrayVar(&o.artifacts, "artifact", nil, "Attach an opaque artifact reference (repeatable)")
	f.StringArrayVar(&o.prsCreated, "pr-created", nil, "Attach a created GitHub PR or GitLab MR (repeatable)")
	f.StringArrayVar(&o.prsReviewed, "pr-reviewed", nil, "Attach a reviewed GitHub PR or GitLab MR (repeatable)")
	f.StringArrayVar(&o.defers, "defer", nil, "File a follow-up you will not do now as an inbox backlog item (repeatable, at most 3, 1-200 characters each)")
	f.BoolVar(&o.checkpoint, "checkpoint", false, "Report a checkpoint")
	f.BoolVar(&o.needsInput, "needs-input", false, "Report that input is needed")
	f.BoolVar(&o.stuck, "stuck", false, "Report that work is stuck")
	f.BoolVar(&o.done, "done", false, "Report that work is done")
	return cmd
}

func (c *commandContext) report(ctx context.Context, out io.Writer, args []string, o reportOptions) error {
	states := []struct {
		on    bool
		state domain.ReportState
	}{{o.checkpoint, domain.ReportCheckpoint}, {o.needsInput, domain.ReportNeedsInput}, {o.stuck, domain.ReportStuck}, {o.done, domain.ReportDone}}
	selected := domain.ReportState("")
	count := 0
	for _, candidate := range states {
		if candidate.on {
			selected = candidate.state
			count++
		}
	}
	if count > 1 {
		return usageError{errors.New("usage: state flags are mutually exclusive")}
	}

	outputs := make([]domain.ReportOutput, 0, len(o.artifacts)+len(o.prsCreated)+len(o.prsReviewed))
	for _, reference := range o.artifacts {
		outputs = append(outputs, domain.ReportOutput{Kind: domain.ReportOutputArtifact, Reference: reference})
	}
	for _, reference := range o.prsCreated {
		outputs = append(outputs, domain.ReportOutput{Kind: domain.ReportOutputPRCreated, Reference: reference})
	}
	for _, reference := range o.prsReviewed {
		outputs = append(outputs, domain.ReportOutput{Kind: domain.ReportOutputPRReviewed, Reference: reference})
	}

	if len(args) == 1 && legacyReportStatus(args[0]) {
		return usageError{errors.New("usage: positional report status is unsupported; use a state flag and --note")}
	}
	if len(args) == 1 && subcommandLikeWord(args[0]) {
		return usageError{fmt.Errorf("usage: %q looks like a subcommand, but report has none; send text with `%s report \"<text>\"` or use a state flag and --note (to list sessions: `%s session ls`)", args[0], brand.CLIName, brand.CLIName)}
	}
	message := strings.Join(args, " ")
	if len(args) > 0 && (selected != "" || o.noteSet || len(outputs) > 0) {
		return usageError{errors.New("usage: free-form text cannot be combined with --note, a state flag, or output options")}
	}
	if selected != "" && strings.TrimSpace(o.note) == "" {
		return usageError{errors.New("usage: --note is required for structured state reports")}
	}
	if selected == "" && o.noteSet {
		return usageError{errors.New("usage: --note requires a structured state flag")}
	}
	if strings.TrimSpace(message) == "" && selected == "" && len(outputs) == 0 {
		return usageError{errors.New("usage: free-form text, a state flag, or at least one output option is required")}
	}
	if utf8.RuneCountInString(message) > domain.MaxReportTextCharacters || utf8.RuneCountInString(o.note) > domain.MaxReportTextCharacters {
		return usageError{errors.New("usage: report text must be at most 1000 characters")}
	}
	deferLines, err := domain.ValidateDeferLines(o.defers)
	if err != nil {
		return usageError{errors.New("usage: --defer takes at most 3 lines, each 1-200 characters")}
	}
	if len(deferLines) > 0 && selected == "" && strings.TrimSpace(message) == "" {
		return usageError{errors.New("usage: --defer must accompany a state flag with --note, or free-form report text")}
	}
	for _, output := range outputs {
		if strings.TrimSpace(output.Reference) == "" {
			return usageError{errors.New("usage: output references must be non-empty")}
		}
		if output.Kind == domain.ReportOutputPRCreated || output.Kind == domain.ReportOutputPRReviewed {
			if _, err := domain.ParseChangeRequestURL(output.Reference); err != nil {
				return usageError{errors.New("usage: --pr-created and --pr-reviewed require complete GitHub PR or GitLab MR URLs")}
			}
		}
	}

	sessionID := strings.TrimSpace(brand.Getenv("SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("usage: TALLPINE_SESSION_ID is required")}
	}
	apiOutputs := make([]apitypes.ReportOutputRequest, len(outputs))
	for i, output := range outputs {
		apiOutputs[i] = apitypes.ReportOutputRequest{Kind: string(output.Kind), Reference: output.Reference}
	}
	var response reportAPIResponse
	if err := c.postJSON(ctx, "reports", apitypes.CreateReportRequest{SessionID: sessionID, State: string(selected), Note: o.note, Message: message, Outputs: apiOutputs, Defer: deferLines}, &response); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, reportConfirmation(sessionID, selected, response.ID, outputs, response.Notice == ""))
	if response.Notice != "" {
		_, _ = fmt.Fprintf(out, "note: %s\n", response.Notice)
	}
	for _, d := range response.Deferred {
		switch {
		case d.Reason != "":
			_, _ = fmt.Fprintf(out, "%s: %s\n", d.Reason, d.Line)
		case d.Status == "merged":
			_, _ = fmt.Fprintf(out, "deferred (already tracked as %s): %s\n", d.Ref, d.Line)
		default:
			_, _ = fmt.Fprintf(out, "deferred as %s: %s\n", d.Ref, d.Line)
		}
	}
	return nil
}

func subcommandLikeWord(value string) bool {
	switch strings.ToLower(value) {
	case "ls", "list", "show", "get":
		return true
	default:
		return false
	}
}

func legacyReportStatus(value string) bool {
	switch strings.ToLower(strings.ReplaceAll(value, "-", "_")) {
	case "working", "checkpoint", "needs_input", "stuck", "done", "pr_created", "pr_reviewed", "artifact":
		return true
	default:
		return false
	}
}

// reportConfirmation is the one-line success receipt, so a silent success is
// never mistaken for a failure and resent.
func reportConfirmation(sessionID string, state domain.ReportState, id string, outputs []domain.ReportOutput, toOrchestrator bool) string {
	label := string(state)
	if label == "" {
		label = "message"
	}
	line := fmt.Sprintf("report sent: state=%s session=%s", label, sessionID)
	if id != "" {
		line += " id=" + id
	}
	for _, o := range outputs {
		line += fmt.Sprintf(" %s=%s", o.Kind, o.Reference)
	}
	if toOrchestrator {
		line += " (delivered to the project orchestrator)"
	}
	return line
}
