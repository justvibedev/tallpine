package cli

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func newPolicyCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Show or change the enterprise agent policy",
	}
	cmd.AddCommand(newPolicyShowCommand(ctx))
	cmd.AddCommand(newPolicySetCommand(ctx))
	return cmd
}

func newPolicyShowCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	var project, session string
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show the effective agent policy and which layer sets each setting",
		Long: "Shows every agent policy rule's settings, their effective values and the layer that set them " +
			"(managed, managed_profile, env, project, user or default). A setting set by managed config or a TALLPINE_AGENT_* variable is locked " +
			"and cannot be changed in Settings; managed_profile marks a floor locked by the managed hardened profile. With --project it shows the policy as it applies to that project, " +
			"including its overrides. See docs/agent-policy.md.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := "settings/agent-policy"
			if project != "" {
				path = "projects/" + url.PathEscape(project) + "/agent-policy"
			}
			if session != "" {
				return showSessionDecision(ctx, cmd, session)
			}
			var p apitypes.AgentPolicyResponse
			if err := ctx.getJSON(cmd.Context(), path, &p); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeJSON(out, p)
			}
			for _, rule := range p.Rules {
				for _, f := range rule.Fields {
					value, _ := json.Marshal(f.Value)
					lock := ""
					if f.Locked {
						lock = ", locked"
					}
					if _, err := fmt.Fprintf(out, "%s.%s: %s (%s%s)\n", rule.Name, f.Name, value, f.Source, lock); err != nil {
						return err
					}
				}
			}
			if p.Profile != "" {
				if _, err := fmt.Fprintf(out, "managed profile: %s (running sessions apply it at their next relaunch or restore)\n", p.Profile); err != nil {
					return err
				}
			}
			if p.ManagedPath != "" {
				if _, err := fmt.Fprintf(out, "managed file: %s\n", p.ManagedPath); err != nil {
					return err
				}
			}
			for _, w := range p.Warnings {
				if _, err := fmt.Fprintf(out, "warning: %s\n", w); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().StringVar(&session, "session", "", "Show the policy decision this session's latest launch was made under, and what it changed")
	cmd.Flags().StringVar(&project, "project", "", "Show the policy as it applies to this project")
	return cmd
}

// showSessionDecision prints the stored decision record of a session: names and
// settings only, answering "why did my agent lose X".
func showSessionDecision(ctx *commandContext, cmd *cobra.Command, session string) error {
	var resp struct {
		SessionID  string `json:"sessionId"`
		RecordedAt string `json:"recordedAt"`
		Record     any    `json:"record"`
	}
	if err := ctx.getJSON(cmd.Context(), "sessions/"+url.PathEscape(session)+"/agent-policy", &resp); err != nil {
		return err
	}
	return writeJSON(cmd.OutOrStdout(), resp)
}
