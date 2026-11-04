package domain

import (
	"strings"
	"testing"
)

func TestParseChangeRequestURL(t *testing.T) {
	tests := []struct {
		input, url, key string
	}{
		{"http://github.com/OWNER/Repo/pull/042/", "https://github.com/owner/repo/pull/42", "github|github.com|owner/repo|42"},
		{"https://www.github.com/Owner/Repo/pull/42", "https://github.com/owner/repo/pull/42", "github|github.com|owner/repo|42"},
		{"https://gitlab.com/group/sub/repo/-/merge_requests/9", "https://gitlab.com/group/sub/repo/-/merge_requests/9", "gitlab|gitlab.com|group/sub/repo|9"},
	}
	for _, tc := range tests {
		got, err := ParseChangeRequestURL(tc.input)
		if err != nil || got.URL != tc.url || got.Key() != tc.key {
			t.Errorf("ParseChangeRequestURL(%q) = %+v, %v", tc.input, got, err)
		}
	}
	invalid := []string{
		"https://github.com/o/r/pull/0", "https://github.com/o/r/issues/1",
		"https://api.github.com/o/r/pull/1", "https://gist.github.com/o/r/pull/1",
		"https://github.com/o/r/pull/1?x=1", "https://github.com/o/r/pull/1#x",
		"https://user:pass@github.com/o/r/pull/1", "https://github.com:123/o/r/pull/1",
		"https://gitlab.com/group/repo/-/merge_requests/nope", "https://gitlab.com/a/-/merge_requests/1",
		"git://github.com/o/r/pull/1", " https://github.com/o/r/pull/1",
		strings.Repeat("x", 2049),
	}
	for _, input := range invalid {
		if got, err := ParseChangeRequestURL(input); err == nil {
			t.Errorf("ParseChangeRequestURL(%q) = %+v, want error", input, got)
		}
	}
}

func TestParsePastedChangeRequestURL(t *testing.T) {
	cases := []struct {
		name, raw, want string // want "" = error
	}{
		{"plain", "https://github.com/o/r/pull/7", "https://github.com/o/r/pull/7"},
		{"www host", "https://www.github.com/o/r/pull/7", "https://github.com/o/r/pull/7"},
		{"api host rejected", "https://api.github.com/o/r/pull/7", ""},
		{"gist subdomain rejected", "https://gist.github.com/o/r/pull/7", ""},
		{"other github.com subdomain rejected", "https://git.github.com/o/r/pull/7", ""},
		{"ghe.com tenant", "https://acme.ghe.com/o/r/pull/7", "https://acme.ghe.com/o/r/pull/7"},
		{"ghe.io dropped", "https://acme.ghe.io/o/r/pull/7", ""},
		{"ghe.com tenant with port rejected", "https://acme.ghe.com:8443/o/r/pull/7", ""},
		{"ghe.com api host rejected", "https://api.acme.ghe.com/o/r/pull/7", ""},
		{"unlisted ghes host is gitlab-shaped", "https://ghes.corp.example/o/r/pull/7", ""},
		{"gitlab host with /pull/ in path", "https://gitlab.example.com/g/pull/r/-/merge_requests/3", "https://gitlab.example.com/g/pull/r/-/merge_requests/3"},
		{"gitlab host github-style path", "https://gitlab.com/o/r/pull/7", ""},
		{"userinfo", "https://user:pw@github.com/o/r/pull/7", ""},
		{"query", "https://github.com/o/r/pull/7?diff=split", "https://github.com/o/r/pull/7"},
		{"fragment", "https://github.com/o/r/pull/7#issuecomment-1", "https://github.com/o/r/pull/7"},
		{"files suffix", "https://github.com/o/r/pull/7/files", "https://github.com/o/r/pull/7"},
		{"files and query", "https://github.com/o/r/pull/7/files?w=1#diff-abc", "https://github.com/o/r/pull/7"},
		{"commits suffix trailing slash", "https://github.com/o/r/pull/7/commits/", "https://github.com/o/r/pull/7"},
		{"gitlab diffs query", "https://gitlab.com/g/s/r/-/merge_requests/9?tab=diffs", "https://gitlab.com/g/s/r/-/merge_requests/9"},
		{"mixed case", "HTTPS://GitHub.com/Owner/Repo/pull/7", "https://github.com/owner/repo/pull/7"},
		{"surrounding space", "  https://github.com/o/r/pull/7\n", "https://github.com/o/r/pull/7"},
		{"http rejected", "http://github.com/o/r/pull/7", ""},
		{"http gitlab rejected", "http://gitlab.com/g/r/-/merge_requests/9", ""},
		{"issues", "https://github.com/o/r/issues/7", ""},
		{"zero", "https://github.com/o/r/pull/0", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePastedChangeRequestURL(tc.raw)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("got %+v, want error", got)
				}
				return
			}
			if err != nil || got.URL != tc.want {
				t.Fatalf("got %+v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestIsGitHubHost(t *testing.T) {
	for host, want := range map[string]bool{
		"github.com": true, "WWW.GitHub.com": true, "api.github.com": true, "x.github.com": true,
		"acme.ghe.com": true, "acme.ghe.io": false, "ghe.com": false, "ghes.corp.example": false, "github.com:443": true, "gitlab.com": false, "github.com.evil.io": false, "": false,
	} {
		if got := IsGitHubHost(host); got != want {
			t.Errorf("IsGitHubHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestParseChangeRequestURLCustomHostRejected(t *testing.T) {
	if _, err := ParseChangeRequestURL("https://ghe.corp.example/org/repo/pull/7"); err == nil {
		t.Fatal("custom GHES host accepted")
	}
}
