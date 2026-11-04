package domain

import (
	"net"
	"strings"
	"sync"
)

// githubEnterpriseHosts holds the self-hosted GHES hosts (lowercase, port
// included when listed) registered at process start from
// TALLPINE_GITHUB_ENTERPRISE_HOSTS. Empty by default.
var (
	githubEnterpriseMu    sync.RWMutex
	githubEnterpriseHosts = map[string]bool{}
)

// SetGitHubEnterpriseHosts replaces the registered GHES hosts. Entries are
// matched exactly (case-insensitive, port included).
func SetGitHubEnterpriseHosts(hosts []string) {
	m := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			m[h] = true
		}
	}
	githubEnterpriseMu.Lock()
	githubEnterpriseHosts = m
	githubEnterpriseMu.Unlock()
}

// IsGitHubEnterpriseListedHost reports whether host (with optional port) is a
// registered GHES host.
func IsGitHubEnterpriseListedHost(host string) bool {
	githubEnterpriseMu.RLock()
	defer githubEnterpriseMu.RUnlock()
	return githubEnterpriseHosts[strings.ToLower(host)]
}

// IsGitHubDotComHost reports github.com and its web/api subdomains.
func IsGitHubDotComHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.ToLower(host)
	return host == "github.com" || strings.HasSuffix(host, ".github.com")
}

// IsGHEComHost reports a <tenant>.ghe.com (data residency) host.
func IsGHEComHost(host string) bool {
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.ToLower(host)
	return strings.HasSuffix(host, ".ghe.com") && len(host) > len(".ghe.com")
}
