package gitlab

import (
	"net/url"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func (p *Provider) parseGitLabRepo(remote string) (ports.SCMRepo, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ports.SCMRepo{}, false
	}

	// ssh://git@host[:port]/owner/repo.git — the host:port is extracted from
	// the URL's Host field so it matches the https:// form exactly.
	if strings.HasPrefix(remote, "ssh://") {
		u, err := url.Parse(remote)
		if err != nil || u.Host == "" {
			return ports.SCMRepo{}, false
		}
		host := u.Host // includes port if present, e.g. "gitlab.internal:8443"
		if !p.isHostAllowed(host) {
			return ports.SCMRepo{}, false
		}
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		owner, name, ok := splitOwnerRepo(path)
		if !ok {
			return ports.SCMRepo{}, false
		}
		return makeGitLabRepo(host, owner, name), true
	}

	// SSH scp-like: git@gitlab.com:owner/repo.git
	if m := sshRemoteRe.FindStringSubmatch(remote); m != nil {
		host := m[1]
		if !p.isHostAllowed(host) {
			return ports.SCMRepo{}, false
		}
		owner, name, ok := splitOwnerRepo(strings.TrimSuffix(m[2], ".git"))
		if !ok {
			return ports.SCMRepo{}, false
		}
		return makeGitLabRepo(host, owner, name), true
	}

	// HTTPS: https://gitlab.com/owner/repo.git
	u, err := url.Parse(remote)
	if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
		host := u.Host // includes port if present, e.g. "gitlab.internal:8443"
		if !p.isHostAllowed(host) {
			return ports.SCMRepo{}, false
		}
		path := strings.TrimPrefix(u.Path, "/")
		path = strings.TrimSuffix(path, ".git")
		owner, name, ok := splitOwnerRepo(path)
		if !ok {
			return ports.SCMRepo{}, false
		}
		return makeGitLabRepo(host, owner, name), true
	}

	return ports.SCMRepo{}, false
}

var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):(.+)$`)

// splitOwnerRepo splits a GitLab project path into owner (namespace, possibly
// nested like "group/subgroup") and name (the final repo segment). A bare
// "owner/repo" yields owner="owner", name="repo"; a nested
// "group/subgroup/repo" yields owner="group/subgroup", name="repo". Single
// segments or empty parts are rejected.
func splitOwnerRepo(p string) (string, string, bool) {
	parts := strings.Split(p, "/")
	if len(parts) < 2 {
		return "", "", false
	}
	for _, seg := range parts {
		if seg == "" {
			return "", "", false
		}
	}
	name := parts[len(parts)-1]
	owner := strings.Join(parts[:len(parts)-1], "/")
	return owner, name, true
}

func makeGitLabRepo(host, owner, name string) ports.SCMRepo {
	return ports.SCMRepo{
		Provider: "gitlab",
		Host:     host,
		Owner:    owner,
		Name:     name,
		Repo:     owner + "/" + name,
	}
}
