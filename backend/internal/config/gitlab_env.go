package config

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// loadEnv reads TALLPINE_GITLAB_ALLOWED_HOSTS and TALLPINE_GITLAB_HOST_TOKENS.
func (c *GitLabConfig) loadEnv() error {
	if raw, ok := brand.LookupEnv("GITLAB_ALLOWED_HOSTS"); ok && raw != "" {
		hosts := make([]string, 0, 4)
		for _, h := range strings.Split(raw, ",") {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			hosts = append(hosts, h)
		}
		c.AllowedHosts = hosts
	}

	if raw, ok := brand.LookupEnv("GITLAB_HOST_TOKENS"); ok && raw != "" {
		tokens, err := parseHostTokenMap("TALLPINE_GITLAB_HOST_TOKENS", raw)
		if err != nil {
			return err
		}
		c.HostTokens = tokens
	}
	return nil
}
