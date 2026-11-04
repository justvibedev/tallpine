package cli

import (
	"bufio"
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

// requestGitHubCredential asks the daemon, as this session (TALLPINE_SESSION_ID, with
// the scoped TALLPINE_SESSION_TOKEN the CLI sends automatically), for a credential. It
// never logs or stores the token.
func (c *commandContext) requestGitHubCredential(ctx context.Context, req apitypes.GitHubCredentialRequest) (apitypes.GitHubCredentialResponse, error) {
	session := strings.TrimSpace(brand.Getenv("SESSION_ID"))
	if session == "" {
		return apitypes.GitHubCredentialResponse{}, usageError{errors.New("usage: TALLPINE_SESSION_ID is required (run this inside a Tallpine session)")}
	}
	var out apitypes.GitHubCredentialResponse
	err := c.postJSON(ctx, "sessions/"+url.PathEscape(session)+"/github-credential", req, &out)
	return out, err
}

// refusalReason turns the daemon's refusal into the one line a developer needs,
// or "" when err is not a refusal (a daemon that is down, a bug).
func refusalReason(err error) string {
	var response apiResponseError
	if !errors.As(err, &response) {
		return ""
	}
	switch response.ErrorBody.Code {
	case "GITHUB_CREDENTIALS_DENIED":
		return "GitHub credentials are disabled by the agent policy"
	case "GITHUB_CREDENTIALS_NOT_ENABLED":
		return "this session holds its own GitHub token; the credential helper is not in use"
	case "GITHUB_HOST_NOT_ALLOWED":
		return "that host is not one of this project's GitHub hosts"
	case "GITHUB_CREDENTIAL_UNAVAILABLE":
		return "no GitHub token is available for this project (set a gh account for it in project settings)"
	}
	return ""
}

func newGitCredentialCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "git-credential <get|store|erase>",
		Short: "git credential helper that asks the daemon for the project's GitHub token",
		Long: "Implements git's credential helper protocol for agent sessions under the agent policy's helper mode. " +
			"`get` asks the daemon (as this session) for the project's GitHub token for the requested host and prints it " +
			"for git to use once; `store` and `erase` do nothing, so no token is ever kept. A refusal is reported on stderr " +
			"and git is given no credential. See docs/agent-policy.md.",
		Hidden:        true,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "get":
				return ctx.gitCredentialGet(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
			case "store", "erase":
				_, _ = io.Copy(io.Discard, cmd.InOrStdin())
				return nil
			}
			return usageError{fmt.Errorf("usage: %s git-credential <get|store|erase>", brand.CLIName)}
		},
	}
}

// gitCredentialGet answers one `get`. Refusals exit 0 with no credential, which
// git treats as "this helper has nothing"; the reason goes to stderr.
func (c *commandContext) gitCredentialGet(ctx context.Context, in io.Reader, out, stderr io.Writer) error {
	attrs := map[string]string{}
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			attrs[key] = value
		}
	}
	host := attrs["host"]
	if host == "" {
		return nil
	}
	cred, err := c.requestGitHubCredential(ctx, apitypes.GitHubCredentialRequest{Host: host, Protocol: attrs["protocol"], Path: attrs["path"]})
	if err != nil {
		if reason := refusalReason(err); reason != "" {
			_, _ = fmt.Fprintf(stderr, "tallpine git-credential: %s\n", reason)
			return nil
		}
		return err
	}
	_, err = fmt.Fprintf(out, "username=%s\npassword=%s\n\n", cred.Username, cred.Token)
	return err
}
