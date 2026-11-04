package review

import (
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// SelectRereviewPR returns the tracked PR named by prURL (matched on URL or
// HTMLURL), or the only tracked PR when prURL is empty.
func SelectRereviewPR(prs []domain.PullRequest, prURL string) (domain.PullRequest, bool) {
	want := strings.TrimSpace(prURL)
	if want == "" && len(prs) == 1 {
		return prs[0], true
	}
	for _, pr := range prs {
		if domain.SameChangeRequest(pr.URL, want) || domain.SameChangeRequest(pr.HTMLURL, want) {
			return pr, true
		}
	}
	return domain.PullRequest{}, false
}

// CheckExpectedHead verifies that an open PR among tracked (the named one, or
// any open PR) is at the expected head and returns the URL of the PR to
// review. It refuses with ErrConflict naming the actual head when the PR is
// somewhere else, and ErrInvalid when the head matches several PRs.
func CheckExpectedHead(tracked []domain.PullRequest, prURL, head string) (string, error) {
	var matched, seen []string
	for _, pr := range tracked {
		if pr.Merged || pr.Closed {
			continue
		}
		if prURL != "" && !domain.SameChangeRequest(pr.URL, prURL) && !domain.SameChangeRequest(pr.HTMLURL, prURL) {
			continue
		}
		sha := strings.ToLower(pr.HeadSHA)
		url := textutil.FirstNonBlank(pr.HTMLURL, pr.URL)
		seen = append(seen, url+" at "+sha)
		if sha != "" && strings.HasPrefix(sha, head) {
			matched = append(matched, url)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return "", fmt.Errorf("%w: expected PR head %s but the provider reports %s; the push may not have landed yet, or a newer commit exists", ErrConflict, head, strings.Join(seen, ", "))
	default:
		return "", fmt.Errorf("%w: head %s matches several PRs; name one with --pr", ErrInvalid, head)
	}
}
