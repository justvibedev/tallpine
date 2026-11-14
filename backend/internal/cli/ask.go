package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

func (c *commandContext) asksPath(ctx context.Context, project string) (string, error) {
	resolved, err := c.resolveSpawnProject(ctx, project)
	if err != nil {
		return "", err
	}
	return "projects/" + url.PathEscape(resolved.ID) + "/asks", nil
}

func newAskCommand(ctx *commandContext) *cobra.Command {
	var project, reason, about, recommended, reask string
	var options []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   `ask "<question>" --option <id>=<label> ... --recommended <id>`,
		Short: "Orchestrator: ask the owner a question that waits in a pinned 'Waiting on you' strip",
		Long: "Files a tracked question for the owner: 2 to 6 options (`--option a=\"Use Postgres\"`), one of them recommended, text up to 500 characters. " +
			"It shows in the owner's chat until answered; the owner's tap comes back to you as `Re Q3: <answer>`. " +
			"Run `tallpine ask ls --all` first: the daemon refuses a question that matches an open one (same --about key or text) or an answered one, " +
			"and tells you the earlier answer. Add --reask \"<why>\" to ask an answered question again because things changed. " +
			"Only orchestrators ask; a worker reports to its orchestrator.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := parseAskOptions(options)
			if err != nil {
				return usageError{err}
			}
			if err := domain.ValidateAsk(strings.TrimSpace(args[0]), reason, about, opts, recommended, reask); err != nil {
				return usageError{err}
			}
			path, err := ctx.asksPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			body := controllers.OwnerAskCreateRequest{Text: args[0], Reason: reason, About: about, Recommended: recommended, Reask: reask}
			for _, o := range opts {
				body.Options = append(body.Options, controllers.PlanOptionDTO{ID: o.ID, Label: o.Label})
			}
			var out controllers.OwnerAskEnvelope
			if err := ctx.postJSON(cmd.Context(), path, body, &out); err != nil {
				return err
			}
			return printAsk(cmd.OutOrStdout(), out.Ask, asJSON, "asked the owner")
		},
	}
	cmd.Flags().StringArrayVar(&options, "option", nil, "Answer choice as id=label; repeat 2 to 6 times")
	cmd.Flags().StringVar(&recommended, "recommended", "", "Id of the option you recommend")
	cmd.Flags().StringVar(&reason, "reason", "", "Why the owner is asked (shown under the question)")
	cmd.Flags().StringVar(&about, "about", "", "Dedupe key for what this is about, such as a backlog ref (B62)")
	cmd.Flags().StringVar(&reask, "reask", "", "Why you ask again: allows repeating an answered question")
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: TALLPINE_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	cmd.AddCommand(newAskListCommand(ctx), newAskAnswerCommand(ctx), newAskWithdrawCommand(ctx))
	return cmd
}

// parseAskOptions reads repeated --option id=label flags.
func parseAskOptions(raw []string) ([]domain.PlanOption, error) {
	out := make([]domain.PlanOption, 0, len(raw))
	for _, r := range raw {
		id, label, ok := strings.Cut(r, "=")
		id, label = strings.TrimSpace(id), strings.TrimSpace(label)
		if !ok || id == "" || label == "" {
			return nil, fmt.Errorf("--option %q must look like id=label", r)
		}
		out = append(out, domain.PlanOption{ID: id, Label: label})
	}
	return out, nil
}

func newAskListCommand(ctx *commandContext) *cobra.Command {
	var project string
	var all, asJSON bool
	cmd := &cobra.Command{
		Use: "ls", Short: "List the project's asks (open ones; --all adds answered and withdrawn)",
		Long: "Run this before asking: every orchestrator of the project shares these asks, and an answered question must not be asked again.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := ctx.asksPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			if all {
				path += "?state=all"
			}
			var out controllers.OwnerAsksResponse
			if err := ctx.getJSON(cmd.Context(), path, &out); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			if len(out.Asks) == 0 {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "no asks")
				return err
			}
			for _, a := range out.Asks {
				if err := printAsk(cmd.OutOrStdout(), a, false, ""); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include answered and withdrawn asks")
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: TALLPINE_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newAskAnswerCommand(ctx *commandContext) *cobra.Command {
	var project, option, text, ownerMessage string
	var asJSON bool
	cmd := &cobra.Command{
		Use: "answer <ref> (--option <id> | --text <answer>) --owner-message <id>|last", Short: "Record the owner's answer to an ask, given in chat",
		Long: "The owner normally answers with a tap on the ask card. When they answered in chat instead, record it here: name the option or give their words with --text, " +
			"and name their message with --owner-message <chat message id>, or `last` for their newest message in the project chat. " +
			"The daemon checks it is the owner's and newer than the ask. Never answer for the owner.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (option == "") == (text == "") {
				return usageError{errors.New("give exactly one of --option and --text")}
			}
			if ownerMessage == "" {
				return usageError{errors.New("--owner-message is required: the owner's chat message that answers (id, or last)")}
			}
			path, err := ctx.asksPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var out controllers.OwnerAskEnvelope
			body := controllers.OwnerAskAnswerRequest{Option: option, Other: text, OwnerMessage: ownerMessage}
			if err := ctx.postJSON(cmd.Context(), path+"/"+url.PathEscape(args[0])+"/answer", body, &out); err != nil {
				return err
			}
			return printAsk(cmd.OutOrStdout(), out.Ask, asJSON, "recorded the answer")
		},
	}
	cmd.Flags().StringVar(&option, "option", "", "Id of the option the owner chose")
	cmd.Flags().StringVar(&text, "text", "", "The owner's own words, when no option fits")
	cmd.Flags().StringVar(&ownerMessage, "owner-message", "", "The owner's chat message that answers (id, or last)")
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: TALLPINE_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newAskWithdrawCommand(ctx *commandContext) *cobra.Command {
	var project, reason string
	var asJSON bool
	cmd := &cobra.Command{
		Use: "withdraw <ref> [--reason <why>]", Short: "Withdraw an open ask that no longer needs an answer",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := ctx.asksPath(cmd.Context(), project)
			if err != nil {
				return err
			}
			var out controllers.OwnerAskEnvelope
			if err := ctx.postJSON(cmd.Context(), path+"/"+url.PathEscape(args[0])+"/withdraw", controllers.OwnerAskWithdrawRequest{Reason: reason}, &out); err != nil {
				return err
			}
			return printAsk(cmd.OutOrStdout(), out.Ask, asJSON, "withdrew")
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why the question is no longer needed")
	cmd.Flags().StringVar(&project, "project", "", "Project id (default: TALLPINE_PROJECT_ID, current session, or current registered repo)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func printAsk(w io.Writer, a controllers.OwnerAskView, asJSON bool, verb string) error {
	if asJSON {
		return writeJSON(w, controllers.OwnerAskEnvelope{Ask: a})
	}
	line := fmt.Sprintf("%s [%s] %s", a.Ref, a.State, a.Text)
	if a.Lane != "" {
		line += " (lane " + a.Lane + ")"
	}
	if a.Answer != nil {
		line += " -> " + a.Answer.Text
	}
	if a.State == "withdrawn" && a.WithdrawReason != "" {
		line += " (withdrawn: " + a.WithdrawReason + ")"
	}
	if a.ReaskOf != "" {
		line += " (asked again after " + a.ReaskOf + ": " + a.ReaskReason + ")"
	}
	if verb != "" {
		line = verb + ": " + line
	}
	_, err := fmt.Fprintln(w, line)
	return err
}
