package domain

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// RepositoryIdentity identifies a provider repository, including its entire namespace.
// Git transport remotes never implicitly authorize additional repositories.
type RepositoryIdentity struct {
	Provider string
	// Host includes an explicit port, matching the SCM provider authority.
	Host      string
	Namespace string
	Name      string
}

// RepositoryProvider follows the SCM dispatcher: recognized GitHub hosts use
// GitHub; other hosts use GitLab, including self-managed installations.
func RepositoryProvider(host string) string {
	if IsGitHubHost(host) {
		return "github"
	}
	return "gitlab"
}

// IsGitHubHost is the single GitHub host rule: github.com, www.github.com,
// api.github.com, *.github.com, *.ghe.com and registered GHES hosts (provider routing). An optional :port and any case
// are ignored. Change-request URL parsing is narrower: github.com, www.github.com, and *.ghe.com only (plus a registered GHES host).
func IsGitHubHost(host string) bool {
	if IsGitHubEnterpriseListedHost(host) {
		return true
	}
	return IsGitHubDotComHost(host) || IsGHEComHost(host)
}

// ParseRepositoryIdentity accepts repository URLs and Git SSH remotes, never PR URLs.
func ParseRepositoryIdentity(raw string) (RepositoryIdentity, error) {
	invalid := errors.New("expected a repository URL with host and full namespace/repository")
	if raw == "" || strings.TrimSpace(raw) != raw {
		return RepositoryIdentity{}, invalid
	}
	sshRemote := strings.HasPrefix(raw, "git@") || strings.HasPrefix(raw, "ssh://")
	if strings.HasPrefix(raw, "git@") {
		host, path, ok := strings.Cut(strings.TrimPrefix(raw, "git@"), ":")
		if !ok {
			return RepositoryIdentity{}, invalid
		}
		raw = "ssh://git@" + host + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil {
		return RepositoryIdentity{}, invalid
	}
	if (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh") || u.Hostname() == "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.ContainsAny(u.Path, `\`) {
		return RepositoryIdentity{}, invalid
	}
	if strings.HasSuffix(u.Host, ":") {
		return RepositoryIdentity{}, invalid
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return RepositoryIdentity{}, invalid
		}
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/"), "/")
	if len(parts) < 2 {
		return RepositoryIdentity{}, invalid
	}
	parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], ".git")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || part == "-" || strings.ContainsAny(part, " \t\r\n?#%") {
			return RepositoryIdentity{}, invalid
		}
	}
	host := u.Host
	if sshRemote && u.Port() == "" && !IsGitHubHost(host) {
		// An ~/.ssh/config alias (git@github-work:o/r) is not the provider host.
		if actual := ResolveSSHHostAlias(host); actual != "" {
			host = actual
		}
	}
	provider := RepositoryProvider(host)
	if provider == "github" && len(parts) != 2 {
		return RepositoryIdentity{}, invalid
	}
	return RepositoryIdentity{Provider: provider, Host: strings.ToLower(host), Namespace: strings.Join(parts[:len(parts)-1], "/"), Name: parts[len(parts)-1]}, nil
}

// ValidateCanonicalRepository checks an explicitly selected canonical
// repository: it must be a credential-free HTTPS repository URL and the project
// must have a valid checkout origin. The canonical repository may live on a
// different host than the origin: that is how a project follows a repository
// that moved to another host (see TrackedRepoURL). Empty canonical identity
// preserves origin-only claims.
func (c ProjectConfig) ValidateCanonicalRepository(origin string) error {
	if c.CanonicalRepoURL == "" {
		return nil
	}
	_, err := ParseRepositoryIdentity(c.CanonicalRepoURL)
	canonicalURL, urlErr := url.Parse(c.CanonicalRepoURL)
	if err != nil || urlErr != nil || canonicalURL.Scheme != "https" || canonicalURL.User != nil ||
		((IsGitHubDotComHost(canonicalURL.Host) || IsGHEComHost(canonicalURL.Host)) && canonicalURL.Port() != "") {
		return fmt.Errorf("canonicalRepoURL: use an HTTPS repository URL without credentials, query, or fragment")
	}
	if _, err := ParseRepositoryIdentity(origin); err != nil {
		return fmt.Errorf("canonicalRepoURL: project must have a valid checkout origin repository")
	}
	return nil
}

// TrackedRepoURL returns the repository URL PR tracking and review should use.
// It is the stored origin, unless the canonical repository is on a different
// provider or host: then the repository moved and the canonical URL wins, since
// the stored origin no longer hosts the project's PRs. A same-host canonical
// URL is a fork's upstream and does not replace the origin.
func (c ProjectConfig) TrackedRepoURL(origin string) string {
	if c.CanonicalRepoURL == "" {
		return origin
	}
	canonical, err := ParseRepositoryIdentity(c.CanonicalRepoURL)
	if err != nil {
		return origin
	}
	if o, err := ParseRepositoryIdentity(origin); err == nil && o.Provider == canonical.Provider && o.Host == canonical.Host {
		return origin
	}
	return c.CanonicalRepoURL
}

// CredentialRepoURL is TrackedRepoURL restricted to GitHub-family hosts: a
// canonical URL on any other host never widens where GitHub credentials are
// offered, so a mistyped or non-GitHub canonical host cannot receive a token.
func (c ProjectConfig) CredentialRepoURL(origin string) string {
	if u, err := ParseRepositoryIdentity(c.CanonicalRepoURL); err != nil || !IsGitHubHost(u.Host) {
		return origin
	}
	return c.TrackedRepoURL(origin)
}

// CredentialRemoteForHost picks the remote an account token is resolved from
// for one credential host: the credential repo (the moved-to canonical GitHub
// repo) only for its own host and github.com, otherwise the stored origin. A
// token for the new repo is therefore never offered to the old origin host.
func (c ProjectConfig) CredentialRemoteForHost(origin, host string) string {
	remote := c.CredentialRepoURL(origin)
	id, err := ParseRepositoryIdentity(remote)
	if err != nil {
		return origin
	}
	bare := func(h string) string {
		if name, _, err := net.SplitHostPort(h); err == nil {
			h = name
		}
		return strings.ToLower(strings.TrimSpace(h))
	}
	h := bare(host)
	// The origin host always resolves from the origin: checkout and push stay there.
	if oid, err := ParseRepositoryIdentity(origin); err == nil && h == bare(oid.Host) {
		return origin
	}
	if h == "github.com" || h == bare(id.Host) {
		return remote
	}
	return origin
}

// AccountRemoteForHost is CredentialRemoteForHost for account resolution, with
// one more rule: on a moved project (canonical differs from origin) a non-GitHub
// origin host resolves no account, because a githubUser override would otherwise
// hand a GitHub token to the old host. A plain project with no canonical keeps
// its override-driven behaviour (e.g. an unregistered GHES origin).
func (c ProjectConfig) AccountRemoteForHost(origin, host string) (string, bool) {
	remote := c.CredentialRemoteForHost(origin, host)
	if remote == origin && c.CredentialRepoURL(origin) != origin {
		if id, err := ParseRepositoryIdentity(origin); err != nil || !IsGitHubHost(id.Host) {
			return "", false
		}
	}
	return remote, true
}

// AccountRemotes lists the remotes a project's GitHub account can be resolved
// from, origin first: after a move the origin may not be a github.com remote
// while the canonical one is, or the reverse.
func (c ProjectConfig) AccountRemotes(origin string) []string {
	if cred := c.CredentialRepoURL(origin); cred != origin {
		return []string{origin, cred}
	}
	return []string{origin}
}

// renamedRepos maps a repository's new full name to the one it had before a
// GitHub rename, lower-cased. GitHub redirects the old name, so PR rows stored
// under either one describe the same repository.
var renamedRepos = map[string]string{
	"justvibedev/tallpine-dev": "justvibedev/aox",
}

// SameRepoPath reports whether two "namespace/name" paths name the same
// repository: case-insensitively, ignoring a ".git" suffix, and treating a
// known renamed repository's old and new names as one.
func SameRepoPath(a, b string) bool {
	canon := func(p string) string {
		p = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(p), ".git"))
		if old, ok := renamedRepos[p]; ok {
			return old
		}
		return p
	}
	return canon(a) == canon(b)
}

// HasHostedRemote reports whether a project repo URL names a hosted remote that
// PRs, pushes and fetches make sense against. An empty origin or a local-folder
// origin (path or file://) is not hosted.
func HasHostedRemote(repo string) bool {
	repo = strings.TrimSpace(repo)
	return repo != "" && !IsLocalPathRemote(repo)
}

// IsLocalPathRemote reports whether a git remote URL points at a folder on this
// machine (absolute or relative path, ~, file://) rather than a hosted service.
func IsLocalPathRemote(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(raw), "file://") {
		return true
	}
	if strings.Contains(raw, "://") {
		return false
	}
	// scp-like remotes (user@host:path, host:path) are not local paths.
	if i := strings.Index(raw, ":"); i > 0 && !strings.ContainsAny(raw[:i], `/\`) && (i != 1 || raw[0] < 'A' || raw[0] > 'z') {
		return false
	}
	return true
}
