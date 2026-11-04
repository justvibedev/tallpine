package importer

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

var importScpRemotePattern = regexp.MustCompile(`^[^/@:\s]+@[^/:\s]+:(.+)$`)

func validateImportRemoteURL(raw string) error {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, "\r\n\t ") || strings.HasPrefix(value, "-") {
		return invalidImportRemoteURL()
	}
	if match := importScpRemotePattern.FindStringSubmatch(value); len(match) == 2 {
		if strings.Trim(match[1], "/\\") == "" {
			return invalidImportRemoteURL()
		}
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return invalidImportRemoteURL()
	}
	scheme := strings.ToLower(parsed.Scheme)
	hasPassword := false
	if parsed.User != nil {
		_, hasPassword = parsed.User.Password()
	}
	hasDisallowedUserinfo := parsed.User != nil && scheme != "ssh"
	if hasDisallowedUserinfo || ((scheme == "http" || scheme == "https") && parsed.RawQuery != "") || hasPassword || hasSensitiveImportRemoteQuery(parsed) {
		return apierr.Invalid("GIT_URL_CONTAINS_CREDENTIALS", "Use your configured Git credentials or an SSH URL instead of putting credentials in the repository URL.", nil)
	}
	switch scheme {
	case "file":
		if len(strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' || r == '\\' })) >= 1 {
			return nil
		}
	case "git", "http", "https", "ssh":
		if parsed.Host != "" && len(strings.FieldsFunc(parsed.Path, func(r rune) bool { return r == '/' || r == '\\' })) >= 1 {
			return nil
		}
	}
	return invalidImportRemoteURL()
}

func hasSensitiveImportRemoteQuery(parsed *url.URL) bool {
	if parsed.RawQuery == "" {
		return false
	}
	for key := range parsed.Query() {
		normalized := strings.ToLower(key)
		for _, marker := range []string{"auth", "credential", "key", "password", "secret", "token"} {
			if strings.Contains(normalized, marker) {
				return true
			}
		}
	}
	return false
}

func invalidImportRemoteURL() error {
	return apierr.Invalid("INVALID_GIT_URL", "Enter a valid HTTPS, SSH, Git, or file repository URL.", nil)
}

var importGhOutputFunc = importGhOutput

var importGitOutputFunc = importGitOutput

func importGhOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := tallpineprocess.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
