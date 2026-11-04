package cli

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// agentModelInfo mirrors the daemon's AgentModelInfo for the CLI client.
type agentModelInfo struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Provider  string `json:"provider,omitempty"`
	IsDefault bool   `json:"isDefault,omitempty"`
}

// agentModelsResponse mirrors GET /api/v1/agents/{agent}/models.
type agentModelsResponse struct {
	AgentID     string           `json:"agentId"`
	AllowCustom bool             `json:"allowCustom"`
	Models      []agentModelInfo `json:"models"`
}

// harnessSettings is the subset of GET /api/v1/settings used for disabled models.
type harnessSettings struct {
	HarnessDefaults map[string]struct {
		DisabledModels []string `json:"disabledModels"`
	} `json:"harnessDefaults"`
}

type agentModelsOutput struct {
	Harness        string           `json:"harness"`
	AllowCustom    bool             `json:"allowCustom"`
	Models         []agentModelInfo `json:"models"`
	DisabledModels []string         `json:"disabledModels"`
	Project        string           `json:"project,omitempty"`
	ProjectModel   string           `json:"projectModel,omitempty"`
}

func agentModelsPath(harness, project string) string {
	path := "agents/" + url.PathEscape(harness) + "/models?catalogOnly=true"
	if project = strings.TrimSpace(project); project != "" {
		path += "&projectId=" + url.QueryEscape(project)
	}
	return path
}

func newAgentModelsCommand(ctx *commandContext) *cobra.Command {
	var project string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "models <harness>",
		Short: "List a harness's exact model IDs, disabled models, and the project default",
		Long: "Prints the model IDs the harness offers (the exact strings `spawn --model` and `review trigger --model` accept), " +
			"the models disabled in Settings, and with --project the project's configured default model.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			harness := strings.TrimSpace(args[0])
			var cat agentModelsResponse
			if err := ctx.getJSON(cmd.Context(), agentModelsPath(harness, project), &cat); err != nil {
				return err
			}
			out := agentModelsOutput{Harness: harness, AllowCustom: cat.AllowCustom, Models: cat.Models, DisabledModels: []string{}}
			var st harnessSettings
			if err := ctx.getJSON(cmd.Context(), "settings", &st); err == nil {
				if d := st.HarnessDefaults[harness].DisabledModels; d != nil {
					out.DisabledModels = d
				}
			}
			if cmd.Flags().Changed("project") {
				p, err := ctx.fetchProjectDetails(cmd.Context(), strings.TrimSpace(project))
				if err != nil {
					return err
				}
				out.Project = p.ID
				if p.Config != nil {
					out.ProjectModel = p.Config.AgentConfig.Model
				}
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), out)
			}
			w := cmd.OutOrStdout()
			tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "MODEL ID\tLABEL\tNOTES")
			disabled := map[string]bool{}
			for _, id := range out.DisabledModels {
				disabled[id] = true
			}
			for _, m := range out.Models {
				var notes []string
				if m.IsDefault {
					notes = append(notes, "harness default")
				}
				if disabled[m.ID] {
					notes = append(notes, "disabled")
				}
				if out.ProjectModel != "" && m.ID == out.ProjectModel {
					notes = append(notes, "project default")
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", m.ID, m.Label, strings.Join(notes, ", "))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if out.Project != "" {
				if out.ProjectModel == "" {
					_, _ = fmt.Fprintf(w, "project %s: no model set (harness default applies)\n", out.Project)
				} else {
					_, _ = fmt.Fprintf(w, "project %s default model: %s\n", out.Project, out.ProjectModel)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Also show this project's default model")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	return cmd
}

// modelCatalogError reports whether model is outside the harness catalog, with
// the valid IDs, even for harnesses that accept direct custom entry (the daemon
// sets allowCustom for claude-code and codex). Skipped (nil) when the catalog
// cannot be read or is empty. Matching is case-insensitive like the daemon.
func modelCatalogError(cat agentModelsResponse, harness, model string) error {
	if model == "" || len(cat.Models) == 0 {
		return nil
	}
	ids := make([]string, 0, len(cat.Models))
	for _, m := range cat.Models {
		if strings.EqualFold(m.ID, model) {
			return nil
		}
		ids = append(ids, m.ID)
	}
	return usageError{fmt.Errorf("unknown model %q for %s; valid model IDs: %s (see `tallpine agent models %s`)", model, harness, strings.Join(ids, ", "), harness)}
}

// validateModel fails fast on a model name outside the harness catalog. An
// unreachable catalog never blocks the command.
func (c *commandContext) validateModel(ctx context.Context, harness, model, project string) error {
	model, harness = strings.TrimSpace(model), strings.TrimSpace(harness)
	if model == "" || harness == "" {
		return nil
	}
	var cat agentModelsResponse
	if err := c.getJSON(ctx, agentModelsPath(harness, project), &cat); err != nil {
		return nil //nolint:nilerr // an unreachable catalog must not block the command
	}
	return modelCatalogError(cat, harness, model)
}

// reviewModelHarness returns the harness and project id a review --model applies to; it is the harness a review --model applies to: --harness, or
// else the project's single configured reviewer harness. Empty when unknown or
// ambiguous (several reviewers), which skips validation.
func (c *commandContext) reviewModelHarness(ctx context.Context, session, harness string) (string, string) {
	sess, err := c.fetchScopedSession(ctx, session, "")
	if err != nil || sess.ProjectID == "" {
		return strings.TrimSpace(harness), ""
	}
	if h := strings.TrimSpace(harness); h != "" {
		return h, string(sess.ProjectID)
	}
	p, err := c.fetchProjectDetails(ctx, string(sess.ProjectID))
	if err != nil || p.Config == nil {
		return "", string(sess.ProjectID)
	}
	return singleReviewerHarness(p.Config.Reviewers), string(sess.ProjectID)
}

func singleReviewerHarness(rs []reviewerConfig) string {
	one := ""
	for _, r := range rs {
		h := strings.TrimSpace(r.Harness)
		if h == "" || (one != "" && h != one) {
			return ""
		}
		one = h
	}
	return one
}
