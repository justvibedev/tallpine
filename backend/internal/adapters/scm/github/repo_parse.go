package github

import (
	"encoding/json"
	"net/url"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func parseGitHubRepo(remote string) (ports.SCMRepo, bool) {
	raw := strings.TrimSpace(remote)
	if raw == "" {
		return ports.SCMRepo{}, false
	}
	if strings.HasPrefix(raw, "git@") {
		raw = strings.TrimPrefix(raw, "git@")
		parts := strings.SplitN(raw, ":", 2)
		if len(parts) != 2 {
			return ports.SCMRepo{}, false
		}
		host := strings.ToLower(parts[0])
		owner, name, ok := splitOwnerRepo(parts[1])
		return makeGitHubRepo(host, owner, name), ok && domain.IsGitHubHost(host)
	}
	if !strings.Contains(raw, "://") && strings.Count(strings.Trim(raw, "/"), "/") == 1 {
		owner, name, ok := splitOwnerRepo(raw)
		return makeGitHubRepo("github.com", owner, name), ok
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ports.SCMRepo{}, false
	}
	host := strings.ToLower(u.Host)
	owner, name, ok := splitOwnerRepo(u.Path)
	return makeGitHubRepo(host, owner, name), ok && domain.IsGitHubHost(host)
}

func splitOwnerRepo(p string) (string, string, bool) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner := parts[0]
	name := strings.TrimSuffix(parts[1], ".git")
	return owner, name, owner != "" && name != ""
}

func makeGitHubRepo(host, owner, name string) ports.SCMRepo {
	return ports.SCMRepo{Provider: "github", Host: host, Owner: owner, Name: name, Repo: owner + "/" + name}
}

func graphQLString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

func repoFullName(repo ports.SCMRepo) string {
	if repo.Repo != "" {
		return repo.Repo
	}
	return repo.Owner + "/" + repo.Name
}
