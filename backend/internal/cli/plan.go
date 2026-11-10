package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type planEnvelopeDTO struct {
	Plan apitypes.PlanView `json:"plan"`
}

func newPlanCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Plan before build: a read-only planner asks, the owner answers and approves",
		Long: "A planner session explores the repo, submits at most 5 questions (each with a recommended answer) and a list of one-line assumptions, " +
			"then writes the plan after the owner answers. The plan is stored on the backlog item; the implementer builds from it. " +
			"Needs TALLPINE_PLANNER on the daemon. Planner: `questions`, `submit`. Orchestrator and owner: the rest.",
	}
	cmd.AddCommand(
		newPlanShowCommand(ctx), newPlanActionCommand(ctx, "start", "Start a planner for an item (Plan now)", ""),
		newPlanActionCommand(ctx, "replan", "Supersede the plan and plan again", "replan"),
		newPlanActionCommand(ctx, "cancel", "Drop the plan that waits on the owner and stop its planner; the item stays open", "cancel"),
		newPlanQuestionsCommand(ctx), newPlanSubmitCommand(ctx), newPlanAnswerCommand(ctx),
		newPlanDefaultsCommand(ctx), newPlanApproveCommand(ctx, "approve", "Approve the submitted plan"),
		newPlanApproveCommand(ctx, "build-anyway", "Build from the approved plan although the base moved"),
	)
	return cmd
}

func (c *commandContext) planPath(ctx context.Context, project, ref, suffix string) (string, error) {
	base, err := c.backlogPath(ctx, project)
	if err != nil {
		return "", err
	}
	p := backlogItemPath(base, ref) + "/plan"
	if suffix != "" {
		p += "/" + suffix
	}
	return p, nil
}

func printPlan(w io.Writer, p apitypes.PlanView, asJSON bool) error {
	if asJSON {
		return writeJSON(w, planEnvelopeDTO{Plan: p})
	}
	_, err := fmt.Fprintf(w, "plan %s: %s\n", p.ID, p.Status)
	return err
}

func newPlanActionCommand(ctx *commandContext, use, short, suffix string) *cobra.Command {
	var project string
	var asJSON bool
	cmd := &cobra.Command{
		Use: use + " <ref>", Short: short, Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := ctx.planPath(cmd.Context(), project, args[0], suffix)
			if err != nil {
				return err
			}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, struct{}{}, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	return cmd
}

func newPlanShowCommand(ctx *commandContext) *cobra.Command {
	var project, planID string
	var asJSON bool
	cmd := &cobra.Command{
		Use: "show <ref>", Short: "Print an item's plan: status, questions, answers, decisions and the plan", Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := ctx.planPath(cmd.Context(), project, args[0], "")
			if err != nil {
				return err
			}
			if planID != "" {
				path += "?planId=" + url.QueryEscape(planID)
			}
			var out planEnvelopeDTO
			if err := ctx.getJSON(cmd.Context(), path, &out); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			return writePlanText(cmd.OutOrStdout(), out.Plan)
		},
	}
	cmd.Flags().StringVar(&planID, "plan-id", "", "Show this plan of the item (a superseded one too) instead of the current plan")
	addBacklogProjectFlag(cmd, &project, &asJSON)
	return cmd
}

func writePlanText(w io.Writer, p apitypes.PlanView) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Plan %s: %s\n", p.ID, p.Status)
	if p.Failure != "" {
		fmt.Fprintf(&b, "Failure: %s\n", p.Failure)
	}
	for _, q := range p.Questions {
		fmt.Fprintf(&b, "\nQuestion %s: %s\n", q.ID, q.Text)
		if q.Reason != "" {
			fmt.Fprintf(&b, "  Why: %s\n", q.Reason)
		}
		for _, o := range q.Options {
			mark := " "
			if o.ID == q.Recommended {
				mark = "*"
			}
			fmt.Fprintf(&b, "  %s %s: %s\n", mark, o.ID, o.Label)
		}
		if a, ok := answerOf(p, q.ID); ok {
			fmt.Fprintf(&b, "  Answer: %s\n", a)
		}
	}
	for _, a := range p.Assumptions {
		fmt.Fprintf(&b, "\nAssumption %s: %s\n", a.ID, a.Text)
		if c, ok := answerCorrections(p)[a.ID]; ok {
			fmt.Fprintf(&b, "  Correction: %s\n", c)
		}
	}
	if p.Answers != nil {
		fmt.Fprintf(&b, "\nAnswered by %s", p.Answers.By)
		if p.Answers.Relayed {
			b.WriteString(" (relayed)")
		}
		b.WriteString("\n")
	}
	if p.DecisionsMarkdown != "" {
		fmt.Fprintf(&b, "\nDecisions:\n%s\n", p.DecisionsMarkdown)
	}
	if p.PlanMarkdown != "" {
		fmt.Fprintf(&b, "\nPlan:\n%s\n", p.PlanMarkdown)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func answerOf(p apitypes.PlanView, id string) (string, bool) {
	if p.Answers == nil {
		return "", false
	}
	a, ok := p.Answers.Questions[id]
	if !ok {
		return "", false
	}
	if a.Other != "" {
		return a.Other, true
	}
	return a.Option, true
}

// readJSONFile decodes one JSON file strictly.
func readJSONFile(path string, dst any) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- the caller names the file
	if err != nil {
		return usageError{fmt.Errorf("read %s: %w", path, err)}
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return usageError{fmt.Errorf("%s: %w", path, err)}
	}
	return nil
}

func readTextFile(path string) (string, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- the caller names the file
	if err != nil {
		return "", usageError{fmt.Errorf("read %s: %w", path, err)}
	}
	return string(raw), nil
}

func newPlanQuestionsCommand(ctx *commandContext) *cobra.Command {
	var project, file string
	var asJSON bool
	cmd := &cobra.Command{
		Use: "questions <ref> --file questions.json", Short: "Planner: submit at most 5 questions and 20 assumptions, once",
		Long: "File format: {\"questions\":[{\"id\":\"q1\",\"text\":\"...\",\"reason\":\"...\",\"options\":[{\"id\":\"a\",\"label\":\"...\"}],\"recommended\":\"a\"}],\"assumptions\":[{\"id\":\"s1\",\"text\":\"Assumed: ...\"}]}. " +
			"More than 5 questions, more than 20 assumptions or more than 6 options per question is rejected.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return usageError{errors.New("--file is required")}
			}
			var in struct {
				Questions   []domain.PlanQuestion   `json:"questions"`
				Assumptions []domain.PlanAssumption `json:"assumptions"`
			}
			if err := readJSONFile(file, &in); err != nil {
				return err
			}
			if err := domain.ValidatePlanQuestions(in.Questions, in.Assumptions); err != nil {
				return usageError{err}
			}
			path, err := ctx.planPath(cmd.Context(), project, args[0], "questions")
			if err != nil {
				return err
			}
			body := map[string]any{"questions": nonNil(in.Questions), "assumptions": nonNil(in.Assumptions)}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, body, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	cmd.Flags().StringVar(&file, "file", "", "JSON file with the questions and assumptions")
	return cmd
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func newPlanSubmitCommand(ctx *commandContext) *cobra.Command {
	var project, planFile, decisionsFile, prsFile, focusFile string
	var asJSON bool
	cmd := &cobra.Command{
		Use: "submit <ref> --plan plan.md --decisions decisions.md --prs prs.json [--focus focus.json]", Short: "Planner: submit the plan, decisions table, PR split and review focus",
		Long: "prs.json: [{\"title\",\"files\":[],\"tests\",\"summary\",\"consumes\":[],\"produces\":[]}]; each entry needs consumes or produces. " +
			"focus.json: a list of at most 5 failure modes the reviewer checks hardest. Plan 60000 bytes, decisions 8000 bytes at most.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if planFile == "" || decisionsFile == "" || prsFile == "" {
				return usageError{errors.New("--plan, --decisions and --prs are required")}
			}
			plan, err := readTextFile(planFile)
			if err != nil {
				return err
			}
			decisions, err := readTextFile(decisionsFile)
			if err != nil {
				return err
			}
			var prs []domain.PlanPR
			if err := readJSONFile(prsFile, &prs); err != nil {
				return err
			}
			var focus []string
			if focusFile != "" {
				if err := readJSONFile(focusFile, &focus); err != nil {
					return err
				}
			}
			if err := domain.ValidatePlanSubmission(plan, decisions, prs, focus); err != nil {
				return usageError{err}
			}
			path, err := ctx.planPath(cmd.Context(), project, args[0], "submit")
			if err != nil {
				return err
			}
			body := map[string]any{"plan": plan, "decisions": decisions, "prs": prs, "focus": nonNil(focus)}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, body, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	f := cmd.Flags()
	f.StringVar(&planFile, "plan", "", "Plan markdown file")
	f.StringVar(&decisionsFile, "decisions", "", "Decisions table markdown file")
	f.StringVar(&prsFile, "prs", "", "PR split JSON file")
	f.StringVar(&focusFile, "focus", "", "Review focus JSON file (at most 5 items)")
	return cmd
}

func newPlanAnswerCommand(ctx *commandContext) *cobra.Command {
	var project, file string
	var relayed, asJSON bool
	cmd := &cobra.Command{
		Use: "answer <ref> --file answers.json [--relayed]", Short: "Answer the questions and correct assumptions",
		Long: "File format: {\"questions\":{\"q1\":{\"option\":\"a\"},\"q2\":{\"other\":\"free text\"}},\"corrections\":{\"s1\":\"the right value\"}}. " +
			"Every question needs an answer. An orchestrator passes on what the owner typed in chat; the record marks it relayed.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if file == "" {
				return usageError{errors.New("--file is required")}
			}
			var in struct {
				Questions   map[string]domain.PlanAnswer `json:"questions"`
				Corrections map[string]string            `json:"corrections"`
			}
			if err := readJSONFile(file, &in); err != nil {
				return err
			}
			path, err := ctx.planPath(cmd.Context(), project, args[0], "answers")
			if err != nil {
				return err
			}
			body := map[string]any{"questions": in.Questions, "corrections": in.Corrections, "relayed": relayed}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, body, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	cmd.Flags().StringVar(&file, "file", "", "JSON file with the answers")
	cmd.Flags().BoolVar(&relayed, "relayed", false, "The answers are what the owner typed in chat, passed on by you")
	return cmd
}

func newPlanDefaultsCommand(ctx *commandContext) *cobra.Command {
	var project string
	var relayed, asJSON bool
	cmd := &cobra.Command{
		Use: "defaults <ref> [--relayed]", Short: "Go with the recommended answers and every assumption", Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := ctx.planPath(cmd.Context(), project, args[0], "defaults")
			if err != nil {
				return err
			}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, map[string]any{"relayed": relayed}, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	cmd.Flags().BoolVar(&relayed, "relayed", false, "The owner said so in chat and you pass it on")
	return cmd
}

func newPlanApproveCommand(ctx *commandContext, name, short string) *cobra.Command {
	var project, ownerMessage string
	var asJSON bool
	cmd := &cobra.Command{
		Use: name + " <ref> [--owner-message <id>|last]", Short: short,
		Long: short + ". The owner approves on the plan card. An orchestrator may only approve after the owner said so in chat, and must name that message " +
			"with --owner-message <chat message id>, or `last` for the owner's newest message in the project chat. The daemon checks that it is the owner's and newer than the plan's last change. Never approve on your own.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := ctx.planPath(cmd.Context(), project, args[0], name)
			if err != nil {
				return err
			}
			var out planEnvelopeDTO
			if err := ctx.postJSON(cmd.Context(), path, map[string]any{"ownerMessage": ownerMessage}, &out); err != nil {
				return err
			}
			return printPlan(cmd.OutOrStdout(), out.Plan, asJSON)
		},
	}
	addBacklogProjectFlag(cmd, &project, &asJSON)
	cmd.Flags().StringVar(&ownerMessage, "owner-message", "", "The owner's chat message that approves (id, or last); required for an agent")
	return cmd
}

func answerCorrections(p apitypes.PlanView) map[string]string {
	if p.Answers == nil {
		return nil
	}
	return p.Answers.Corrections
}
