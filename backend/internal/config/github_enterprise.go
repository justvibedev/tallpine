package config

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// GitHubEnterpriseConfig carries self-hosted GHES hosts and per-host tokens.
// *.ghe.com tenants need no listing. Loaded once from the environment.
type GitHubEnterpriseConfig struct {
	// Hosts are GHES hosts (lowercase, each may include :port).
	Hosts []string
	// HostTokens maps a host to a token; unlisted hosts use `gh auth token --hostname`.
	HostTokens map[string]string
}

// ParseGitHubEnterpriseHosts splits a comma list, trimming, lowercasing and
// skipping empty entries.
func ParseGitHubEnterpriseHosts(raw string) []string {
	var hosts []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// LoadGitHubEnterprise reads TALLPINE_GITHUB_ENTERPRISE_HOSTS and
// TALLPINE_GITHUB_HOST_TOKENS.
func LoadGitHubEnterprise() (GitHubEnterpriseConfig, error) {
	var out GitHubEnterpriseConfig
	if raw, ok := brand.LookupEnv("GITHUB_ENTERPRISE_HOSTS"); ok {
		out.Hosts = ParseGitHubEnterpriseHosts(raw)
	}
	if raw, ok := brand.LookupEnv("GITHUB_HOST_TOKENS"); ok && raw != "" {
		tokens, err := parseHostTokenMap("TALLPINE_GITHUB_HOST_TOKENS", raw)
		if err != nil {
			return out, err
		}
		out.HostTokens = make(map[string]string, len(tokens))
		for h, t := range tokens {
			out.HostTokens[strings.ToLower(h)] = t
		}
	}
	return out, nil
}
