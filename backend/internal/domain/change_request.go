package domain

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalidChangeRequestURL reports an unsupported or malformed PR/MR URL.
var ErrInvalidChangeRequestURL = errors.New("invalid pull request or merge request URL")

// ChangeRequestReference is a normalized, reference-only PR/MR identity.
type ChangeRequestReference struct {
	URL        string
	Provider   string
	Host       string
	Repository string
	Number     int
}

// Key returns the provider-neutral identity used to deduplicate references.
func (r ChangeRequestReference) Key() string {
	return fmt.Sprintf("%s|%s|%s|%d", r.Provider, r.Host, r.Repository, r.Number)
}

// ParseChangeRequestURL accepts complete GitHub PR and GitLab MR web URLs.
// It never fetches the URL or grants SCM tracking authority.
func ParseChangeRequestURL(raw string) (ChangeRequestReference, error) {
	if raw == "" || len(raw) > 2048 || strings.TrimSpace(raw) != raw {
		return ChangeRequestReference{}, ErrInvalidChangeRequestURL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return ChangeRequestReference{}, ErrInvalidChangeRequestURL
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	provider := RepositoryProvider(u.Host)
	hostname := strings.ToLower(u.Hostname())
	if provider == "github" && !allowedGitHubPRHost(strings.ToLower(u.Host), hostname, u.Port()) {
		return ChangeRequestReference{}, ErrInvalidChangeRequestURL
	}
	var repoParts []string
	var numberText string
	switch {
	case provider == "github" && len(parts) == 4 && parts[2] == "pull":
		repoParts, numberText = parts[:2], parts[3]
	case provider == "gitlab" && len(parts) >= 5 && parts[len(parts)-3] == "-" && parts[len(parts)-2] == "merge_requests":
		repoParts, numberText = parts[:len(parts)-3], parts[len(parts)-1]
	default:
		return ChangeRequestReference{}, ErrInvalidChangeRequestURL
	}
	number, err := strconv.Atoi(numberText)
	if err != nil || number <= 0 {
		return ChangeRequestReference{}, ErrInvalidChangeRequestURL
	}
	repo, err := ParseRepositoryIdentity(u.Scheme + "://" + u.Host + "/" + strings.Join(repoParts, "/"))
	if err != nil {
		return ChangeRequestReference{}, ErrInvalidChangeRequestURL
	}
	name := repo.Namespace + "/" + repo.Name
	host := repo.Host
	if provider == "github" {
		name = strings.ToLower(name)
		u.Scheme = "https"
		if host == "www.github.com" {
			host = "github.com"
		}
	}
	path := "/" + name + "/pull/" + strconv.Itoa(number)
	if provider == "gitlab" {
		path = "/" + name + "/-/merge_requests/" + strconv.Itoa(number)
	}
	return ChangeRequestReference{URL: u.Scheme + "://" + host + path, Provider: provider, Host: host, Repository: name, Number: number}, nil
}

// allowedGitHubPRHost is the PR-URL host rule: github.com, www.github.com and
// *.ghe.com tenants (never api.*, never a port), or a registered GHES host
// matched exactly including its port.
func allowedGitHubPRHost(authority, hostname, port string) bool {
	if IsGitHubEnterpriseListedHost(authority) {
		return true
	}
	if port != "" {
		return false
	}
	if hostname == "github.com" || hostname == "www.github.com" {
		return true
	}
	return !strings.HasPrefix(hostname, "api.") && IsGHEComHost(hostname)
}

// pastedURLSuffixes are PR/MR page tabs a browser URL may carry after the number.
var pastedURLSuffixes = []string{"/files", "/commits", "/checks"}

// ParsePastedChangeRequestURL parses a PR/MR URL copied from a browser: it
// trims whitespace, drops the query and fragment, and drops a trailing
// /files, /commits or /checks tab before ParseChangeRequestURL. It is the only
// entry point for user-typed PR/MR references.
func ParsePastedChangeRequestURL(raw string) (ChangeRequestReference, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < len("https://") || !strings.EqualFold(raw[:len("https://")], "https://") {
		return ChangeRequestReference{}, ErrInvalidChangeRequestURL
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimRight(raw, "/")
	for _, suffix := range pastedURLSuffixes {
		if strings.HasSuffix(raw, suffix) {
			raw = strings.TrimRight(strings.TrimSuffix(raw, suffix), "/")
			break
		}
	}
	return ParseChangeRequestURL(raw)
}

// RepoOwnerAndName splits a reference's "namespace/name" repository at the
// last slash.
func (r ChangeRequestReference) RepoOwnerAndName() (owner, name string) {
	i := strings.LastIndex(r.Repository, "/")
	if i < 0 {
		return "", r.Repository
	}
	return r.Repository[:i], r.Repository[i+1:]
}

// ChangeRequestURL builds the canonical web URL for repoPath ("namespace/name")
// on host: /pull/N for GitHub hosts, /-/merge_requests/N otherwise.
func ChangeRequestURL(host, repoPath string, number int) string {
	if IsGitHubHost(host) {
		return fmt.Sprintf("https://%s/%s/pull/%d", host, repoPath, number)
	}
	return fmt.Sprintf("https://%s/%s/-/merge_requests/%d", host, repoPath, number)
}

// SameChangeRequest reports whether two PR/MR URLs name the same change
// request: identical strings, or equal ChangeRequestReference.Key() after
// parsing. Legacy stored URLs keep their original case, so raw == is not enough.
func SameChangeRequest(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := ParsePastedChangeRequestURL(a)
	rb, errB := ParsePastedChangeRequestURL(b)
	return errA == nil && errB == nil && ra.Key() == rb.Key()
}

// CanonicalChangeRequestURL returns the canonical form of a parseable URL, or
// raw unchanged when it does not parse.
func CanonicalChangeRequestURL(raw string) string {
	if ref, err := ParsePastedChangeRequestURL(raw); err == nil {
		return ref.URL
	}
	return raw
}
