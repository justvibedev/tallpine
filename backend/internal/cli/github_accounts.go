package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// newGitHubCommand is `tallpine github`: read-only views of the machine's gh
// sign-ins. It never switches, adds or removes an account; `gh auth switch`,
// `login` and `logout` change the machine default for every session.
func newGitHubCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "github",
		Short: "Inspect the GitHub accounts signed in on this machine",
	}
	cmd.AddCommand(newGitHubAccountsCommand(ctx))
	return cmd
}

// githubAccountsDTO mirrors the daemon's GitHubAccountsResponse. Logins only.
type githubAccountsDTO struct {
	Accounts []struct {
		Login   string `json:"login"`
		CanPush string `json:"canPush"`
	} `json:"accounts"`
	Repo        string `json:"repo"`
	Override    string `json:"override"`
	AutoPick    string `json:"autoPick"`
	Unavailable bool   `json:"unavailable"`
}

// githubAccountsPath is the project route when a project is known, else the
// machine-wide one.
func githubAccountsPath(project string) string {
	if project == "" {
		return "github/accounts"
	}
	return "projects/" + url.PathEscape(project) + "/github-accounts"
}

func newGitHubAccountsCommand(ctx *commandContext) *cobra.Command {
	var project string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "List the GitHub accounts signed in to gh",
		Long: "Lists the accounts `gh` is signed in to for github.com (logins only, never tokens). With a project " +
			"(--project, or TALLPINE_PROJECT_ID) it also says whether each account can push to that project's " +
			"repository, which account is pinned, and which one auto-pick chooses. To pin one: " +
			"`tallpine project set-config <id> --github-user <login>`; `--github-user \"\"` returns to auto-pick. " +
			"The change reaches sessions started afterwards, not running ones. Do not use `gh auth switch`, `login` or " +
			"`logout`: they change the machine default for every session.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if project = strings.TrimSpace(project); project == "" {
				project = strings.TrimSpace(brand.Getenv("PROJECT_ID"))
			}
			var res githubAccountsDTO
			if err := ctx.getJSON(cmd.Context(), githubAccountsPath(project), &res); err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeGitHubAccounts(cmd.OutOrStdout(), project, res)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "Project id to check push access against (default: TALLPINE_PROJECT_ID)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func writeGitHubAccounts(out io.Writer, project string, res githubAccountsDTO) error {
	if res.Unavailable {
		_, err := fmt.Fprintln(out, "Could not list accounts: `gh` is not installed or did not answer.")
		return err
	}
	if len(res.Accounts) == 0 {
		_, err := fmt.Fprintln(out, "No GitHub account is signed in to gh for github.com.")
		return err
	}
	for _, a := range res.Accounts {
		line := a.Login
		switch a.CanPush {
		case "yes":
			line += "  can push to " + res.Repo
		case "no":
			line += "  cannot push to " + res.Repo
		case "unknown":
			line += "  push access to " + res.Repo + " unknown"
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	if project == "" {
		return nil
	}
	var err error
	switch {
	case res.Override != "":
		_, err = fmt.Fprintf(out, "Project %s is pinned to %s.\n", project, res.Override)
	case res.AutoPick != "":
		_, err = fmt.Fprintf(out, "Project %s uses auto-pick, which chooses %s.\n", project, res.AutoPick)
	case res.Repo == "":
		_, err = fmt.Fprintf(out, "Project %s uses auto-pick, but its origin is not on github.com, so the machine default applies unless an account is pinned.\n", project)
	default:
		_, err = fmt.Fprintf(out, "Project %s uses auto-pick, which has no pick right now: the machine default applies.\n", project)
	}
	return err
}

// githubAccountNotice is printed after a pin or unpin: sessions read their
// account when they start.
const githubAccountNotice = "Applies to sessions started from now on; running sessions keep the account they started with."

// requireSignedInGitHubUser refuses a login gh is not signed in to, so a typo
// does not silently leave the project's sessions without credentials, and
// returns the login spelled as gh lists it (gh looks accounts up by exact name).
// When gh cannot be asked it only warns on stderr and returns login unchanged.
func requireSignedInGitHubUser(ctx context.Context, c *commandContext, login string, warn io.Writer) (string, error) {
	var res githubAccountsDTO
	if listed := c.getJSON(ctx, githubAccountsPath(""), &res) == nil; !listed || res.Unavailable {
		_, _ = fmt.Fprintf(warn, "warning: could not check that %s is signed in to gh\n", login)
		return login, nil
	}
	names := make([]string, 0, len(res.Accounts))
	for _, a := range res.Accounts {
		if strings.EqualFold(a.Login, login) {
			return a.Login, nil
		}
		names = append(names, a.Login)
	}
	have := "none are signed in"
	if len(names) > 0 {
		have = "signed in: " + strings.Join(names, ", ")
	}
	return "", usageError{errors.New("--github-user " + login + " is not signed in to gh (" + have + "); list them with `tallpine github accounts`")}
}

// setGitHubUserOnly pins or clears the project's account through the settings
// registry, the one route an orchestrator may use on its own project.
func setGitHubUserOnly(ctx context.Context, c *commandContext, cmd *cobra.Command, id, login string, asJSON bool) error {
	patch := apitypes.UpdateSettingsValuesRequest{Project: id}
	if login == "" {
		patch.Unset = []string{"project.githubUser"}
	} else {
		patch.Set = map[string]any{"project.githubUser": login}
	}
	var resp apitypes.SettingsValuesResponse
	if err := c.patchJSON(ctx, "settings/values", patch, &resp); err != nil {
		return err
	}
	// Print the same project document as the config route, whichever route wrote it.
	var res projectResult
	if err := c.getJSON(ctx, "projects/"+url.PathEscape(id), &res); err != nil {
		return err
	}
	if err := reportProjectConfig(cmd, projectSetConfigOptions{json: asJSON}, res); err != nil || asJSON {
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), githubAccountNotice)
	return err
}
