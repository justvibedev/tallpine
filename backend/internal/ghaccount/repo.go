// Package ghaccount picks, per GitHub repository, the signed-in `gh` account
// that can push to it. It stores logins only: tokens are fetched on demand from
// `gh auth token -u <login>` and are never logged or persisted.
package ghaccount

import (
	"context"
	"net/url"
	"strings"

	"tallpine.eu/tallpine/backend/internal/sshconfig"
)

// githubHost is the only host handled: `gh` accounts are listed per host and
// GitHub Enterprise and GitLab remotes keep their existing credentials.
const githubHost = "github.com"

// Repo is a github.com repository.
type Repo struct {
	Owner string
	Name  string
}

// Key is the stable lowercase owner/name used for caching.
func (r Repo) Key() string { return strings.ToLower(r.Owner + "/" + r.Name) }

// SSHHostFunc resolves an SSH config alias to its real HostName.
type SSHHostFunc func(ctx context.Context, alias string) (string, error)

// ExecSSHHost reads HostName from `ssh -G <alias>`.
func ExecSSHHost(ctx context.Context, alias string) (string, error) {
	return sshconfig.HostName(ctx, alias)
}

// ParseRemote extracts the github.com repo from a git remote URL. A host other
// than github.com is treated as an SSH alias and accepted only when its
// HostName (via sshHost) is github.com. Everything else reports false.
func ParseRemote(ctx context.Context, remote string, sshHost SSHHostFunc) (Repo, bool) {
	host, path, ok := splitRemote(strings.TrimSpace(remote))
	if !ok {
		return Repo{}, false
	}
	if strings.HasPrefix(host, "-") {
		return Repo{}, false // would be read as an ssh option
	}
	if host != githubHost {
		// ssh -G echoes an unknown host back unchanged, so GitLab, GHE and
		// plain hosts fail the HostName check below.
		if sshHost == nil {
			return Repo{}, false
		}
		if resolved, err := sshHost(ctx, host); err != nil || resolved != githubHost {
			return Repo{}, false
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		return Repo{}, false
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	if owner == "" || name == "" {
		return Repo{}, false
	}
	return Repo{Owner: owner, Name: name}, true
}

func splitRemote(raw string) (host, path string, ok bool) {
	if raw == "" {
		return "", "", false
	}
	if !strings.Contains(raw, "://") {
		// scp-like: [user@]host:owner/repo
		rest := raw
		if at := strings.LastIndex(rest, "@"); at >= 0 {
			rest = rest[at+1:]
		}
		h, p, found := strings.Cut(rest, ":")
		if !found || h == "" || strings.Contains(h, "/") {
			return "", "", false
		}
		return strings.ToLower(h), p, true
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", "", false
	}
	return strings.ToLower(u.Hostname()), u.Path, true
}
