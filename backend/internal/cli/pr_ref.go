package cli

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func (c *commandContext) resolvePRRef(ctx context.Context, ref string, project projectDetails) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", usageError{errors.New("PR reference must be a PR/MR URL or a number")}
	}
	if isNumericPRRef(ref) {
		repo := strings.TrimSpace(project.Repo)
		if project.Config != nil && project.Config.CanonicalRepoURL != "" {
			repo = project.Config.CanonicalRepoURL
		}
		if repo == "" {
			// The daemon must not shell out to external CLIs from its loopback API;
			// when the durable project record lacks repo_origin_url, the thin CLI
			// does the one-off gh lookup from the registered project checkout and
			// sends the daemon a normalized URL.
			out, err := c.deps.CommandOutputInDir(ctx, project.Path, "gh", "repo", "view", "--json", "url", "-q", ".url")
			if err != nil || strings.TrimSpace(string(out)) == "" {
				return "", usageError{errors.New("gh not available; pass the full PR URL")}
			}
			repo = strings.TrimSpace(string(out))
		}
		host, owner, name, err := cliRepoFromURL(repo)
		if err != nil {
			return "", usageError{errors.New("PR reference must be a PR/MR URL or a number")}
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(ref, "#"))
		return domain.CanonicalChangeRequestURL(domain.ChangeRequestURL(host, owner+"/"+name, n)), nil
	}
	parsed, err := domain.ParsePastedChangeRequestURL(ref)
	if err != nil {
		return "", usageError{errors.New("PR reference must be a PR/MR URL or a number")}
	}
	return parsed.URL, nil
}

func isNumericPRRef(ref string) bool {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	n, err := strconv.Atoi(ref)
	return err == nil && n > 0
}

func cliRepoFromURL(raw string) (host, owner, name string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", "", errors.New("empty repo")
	}
	if strings.HasPrefix(raw, "git@") {
		rest := strings.TrimPrefix(raw, "git@")
		colonIdx := strings.Index(rest, ":")
		if colonIdx < 0 {
			return "", "", "", errors.New("bad ssh remote")
		}
		host = rest[:colonIdx]
		path := rest[colonIdx+1:]
		parts := strings.Split(strings.TrimSuffix(path, ".git"), "/")
		if len(parts) < 2 {
			return "", "", "", errors.New("bad repo")
		}
		for _, seg := range parts {
			if seg == "" {
				return "", "", "", errors.New("bad repo")
			}
		}
		name = parts[len(parts)-1]
		owner = strings.Join(parts[:len(parts)-1], "/")
		return host, owner, name, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", "", err
	}
	host = u.Host
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", "", errors.New("bad repo")
	}
	for _, seg := range parts {
		if seg == "" {
			return "", "", "", errors.New("bad repo")
		}
	}
	name = strings.TrimSuffix(parts[len(parts)-1], ".git")
	owner = strings.Join(parts[:len(parts)-1], "/")
	return host, owner, name, nil
}
