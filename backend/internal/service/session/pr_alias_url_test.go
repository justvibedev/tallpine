package session

import "testing"

func TestPullRequestAliasKeyURLForms(t *testing.T) {
	const want = "github|github.com|o|r|7"
	for _, tc := range []struct {
		name, url, want string
	}{
		{"plain", "https://github.com/o/r/pull/7", want},
		{"www host folds", "https://www.github.com/o/r/pull/7", want},
		{"mixed case", "https://GitHub.com/O/R/pull/7", want},
		{"ghe.com tenant", "https://t.ghe.com/o/r/pull/7", "github|t.ghe.com|o|r|7"},
		{"issues url has no key", "https://github.com/o/r/issues/7", ""},
		{"files suffix has no key", "https://github.com/o/r/pull/7/files", ""},
		{"gist subdomain has no key", "https://gist.github.com/o/r/pull/7", ""},
		{"gitlab has no key", "https://gitlab.com/o/r/-/merge_requests/7", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pullRequestAliasKey(tc.url, 7, "", "", ""); got != tc.want {
				t.Fatalf("pullRequestAliasKey(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
