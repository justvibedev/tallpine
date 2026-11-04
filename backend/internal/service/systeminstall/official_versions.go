package systeminstall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// officialReleaseSource marks an advisory compared against the vendor's own
// release channel. It is deliberately not an install method ID: a matching
// version does not prove which installer produced the binary, so clients must
// not pick an update method from it.
const officialReleaseSource = "official-release"

// errNoOfficialSource means Tallpine knows no vendor release channel for a harness.
var errNoOfficialSource = errors.New("systeminstall: no official release source")

type officialSourceKind int

const (
	// officialText is a plain-text body holding only the version.
	officialText officialSourceKind = iota
	// officialJSON is a JSON object with the version in one string field.
	officialJSON
	// officialGitHub is a GitHub repository's latest release tag.
	officialGitHub
	// officialPyPI is a PyPI project's current release.
	officialPyPI
	// officialNPM is an npm package whose release matches the native build.
	officialNPM
	// officialPage is a document that embeds the version in a URL.
	officialPage
)

// officialSource is where a vendor's installer resolves its latest release.
// Each entry mirrors the lookup the vendor's own install script performs.
type officialSource struct {
	kind  officialSourceKind
	ref   string
	field string
	match *regexp.Regexp
}

func officialSourceFor(target Target) (officialSource, bool) {
	switch target {
	case TargetClaudeCode:
		return officialSource{kind: officialText, ref: "https://downloads.claude.ai/claude-code-releases/latest"}, true
	case TargetCodex:
		return officialSource{kind: officialJSON, ref: "https://releases.openai.com/codex/channels/latest", field: "tag_name"}, true
	case TargetKiro:
		return officialSource{kind: officialJSON, ref: "https://prod.download.cli.kiro.dev/stable/latest/manifest.json", field: "version"}, true
	default:
		return officialSource{}, false
	}
}

// officialReleaseVersion fetches the vendor release channel for a harness and
// returns a bare version such as 1.2.3 or 1.4.3-R5018.1.
func officialReleaseVersion() func(context.Context, Target) (string, error) {
	return officialReleaseVersionWith(&http.Client{Timeout: 6 * time.Second})
}

func officialReleaseVersionWith(client *http.Client) func(context.Context, Target) (string, error) {
	return func(ctx context.Context, target Target) (string, error) {
		source, ok := officialSourceFor(target)
		if !ok {
			return "", errNoOfficialSource
		}
		var raw string
		var err error
		switch source.kind {
		case officialPyPI:
			raw, err = pypiLatestRelease(ctx, client, source.ref)
		case officialNPM:
			raw, err = npmLatestTag(ctx, client, source.ref)
		case officialGitHub:
			if tag, tagErr := githubLatestReleaseTag(ctx, client, source.ref); tagErr == nil {
				return parseOfficialVersion(officialSource{kind: officialText}, []byte(tag))
			}
			// Fall back to the REST API, which also follows renamed repositories.
			body, fetchErr := fetchRegistry(ctx, client, "https://api.github.com/repos/"+source.ref+"/releases/latest", "application/vnd.github+json")
			if fetchErr != nil {
				return "", fetchErr
			}
			return parseOfficialVersion(source, body)
		default:
			body, fetchErr := fetchRegistry(ctx, client, source.ref, "")
			if fetchErr != nil {
				return "", fetchErr
			}
			return parseOfficialVersion(source, body)
		}
		if err != nil {
			return "", err
		}
		return parseOfficialVersion(officialSource{kind: officialText}, []byte(raw))
	}
}

// parseOfficialVersion reads the version from a release document. PyPI and
// npm sources are read through the registry lookups shared with package
// updates, so their raw version is parsed as officialText.
func parseOfficialVersion(source officialSource, body []byte) (string, error) {
	var raw string
	switch source.kind {
	case officialText:
		raw = strings.TrimSpace(string(body))
	case officialJSON, officialGitHub:
		field := source.field
		if source.kind == officialGitHub {
			field = "tag_name"
		}
		var document map[string]any
		if err := json.Unmarshal(body, &document); err != nil {
			return "", err
		}
		raw, _ = document[field].(string)
	case officialPage:
		if found := source.match.FindSubmatch(body); found != nil {
			raw = string(found[1])
		}
	}
	// Tags such as v1.2.3 and rust-v0.160.1 carry the version after a prefix.
	version, ok := versionSemver.find(raw)
	if !ok {
		return "", fmt.Errorf("no version in official release metadata %q", raw)
	}
	return version.display, nil
}

// githubLatestReleaseTag reads the tag that github.com/<repo>/releases/latest
// redirects to. Like the REST endpoint it skips drafts and prereleases, but it
// is not subject to the REST API's 60 requests per hour unauthenticated limit
// and returns no body.
func githubLatestReleaseTag(ctx context.Context, client *http.Client, repo string) (string, error) {
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://github.com/"+repo+"/releases/latest", http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := noFollow.Do(request)
	if err != nil {
		return "", err
	}
	_ = response.Body.Close()
	if response.StatusCode < 300 || response.StatusCode > 399 {
		return "", fmt.Errorf("github releases/latest for %s returned status %d", repo, response.StatusCode)
	}
	location, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		return "", err
	}
	// A repository without releases redirects to its releases list instead.
	prefix := strings.ToLower("/" + repo + "/releases/tag/")
	if !strings.HasPrefix(strings.ToLower(location.Path), prefix) {
		return "", fmt.Errorf("github releases/latest for %s redirected to %q", repo, location.Path)
	}
	tag, err := url.PathUnescape(location.Path[len(prefix):])
	if err != nil || tag == "" || strings.Contains(tag, "/") {
		return "", fmt.Errorf("github releases/latest for %s has no release tag", repo)
	}
	return tag, nil
}
