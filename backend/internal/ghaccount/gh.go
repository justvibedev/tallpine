package ghaccount

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// GH is the slice of the `gh` CLI the resolver needs. Tests inject a fake.
type GH interface {
	// Logins lists the accounts signed in to github.com.
	Logins(ctx context.Context) ([]string, error)
	// Token returns the OAuth token of one signed-in account.
	Token(ctx context.Context, login string) (string, error)
}

// ExecGH shells out to the real `gh` binary.
type ExecGH struct{}

var accountLine = regexp.MustCompile(`account ([A-Za-z0-9][A-Za-z0-9-]*)`)

// Logins parses `gh auth status`. Accounts with a broken token are listed too;
// they fail at the token or permission step and are skipped there.
func (ExecGH) Logins(ctx context.Context) ([]string, error) {
	// A non-zero exit still carries the account list, so the error is ignored
	// when output was produced.
	cmd := tallpineprocess.CommandContext(ctx, "gh", "auth", "status", "--hostname", githubHost)
	cmd.Env = ScrubbedEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if len(out) == 0 && err != nil {
		return nil, err
	}
	return parseLogins(string(out)), nil
}

func parseLogins(text string) []string {
	seen := map[string]bool{}
	var logins []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "Logged in to") && !strings.Contains(line, "Failed to log in") {
			continue
		}
		if m := accountLine.FindStringSubmatch(line); len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			logins = append(logins, m[1])
		}
	}
	return logins
}

// Token runs `gh auth token -u <login>`.
func (ExecGH) Token(ctx context.Context, login string) (string, error) {
	cmd := tallpineprocess.CommandContext(ctx, "gh", "auth", "token", "--hostname", githubHost, "-u", login)
	cmd.Env = ScrubbedEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ErrNoToken means gh produced no token for the account.
var ErrNoToken = errors.New("ghaccount: no token for account")

// ErrInvalidLogin rejects a login that is not a plausible GitHub username, so
// nothing odd (a leading dash, whitespace) reaches `gh auth token -u`.
var ErrInvalidLogin = errors.New("ghaccount: invalid GitHub login")

// tokenEnvVars make gh use that token and ignore the keyring account (and `-u`).
var tokenEnvVars = map[string]bool{
	"GH_TOKEN": true, "GITHUB_TOKEN": true, "GH_ENTERPRISE_TOKEN": true, "GITHUB_ENTERPRISE_TOKEN": true,
}

// ScrubbedEnv drops the token variables from env so `gh auth token -u <login>`
// returns that account's keyring token, not the daemon's pinned one.
func ScrubbedEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if tokenEnvVars[strings.ToUpper(name)] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ErrWrongAccount means a token fetched for one login authenticates as another.
var ErrWrongAccount = errors.New("ghaccount: token belongs to a different GitHub account")
