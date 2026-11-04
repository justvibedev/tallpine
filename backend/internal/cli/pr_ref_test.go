package cli

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestCLIRepoFromURL(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantHost  string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{"github https", "https://github.com/owner/repo.git", "github.com", "owner", "repo", false},
		{"github https no suffix", "https://github.com/owner/repo", "github.com", "owner", "repo", false},
		{"gitlab https", "https://gitlab.com/castai/ctxd.git", "gitlab.com", "castai", "ctxd", false},
		{"gitlab nested namespace", "https://gitlab.com/group/subgroup/repo.git", "gitlab.com", "group/subgroup", "repo", false},
		{"gitlab deep nested namespace", "https://gitlab.com/group/sub1/sub2/repo.git", "gitlab.com", "group/sub1/sub2", "repo", false},
		{"gitlab self-managed nested", "https://gitlab.mycompany.com/eng/team/repo.git", "gitlab.mycompany.com", "eng/team", "repo", false},
		{"ssh remote", "git@gitlab.com:group/subgroup/repo.git", "gitlab.com", "group/subgroup", "repo", false},
		{"ssh single group", "git@github.com:owner/repo.git", "github.com", "owner", "repo", false},
		{"empty", "", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, owner, repo, err := cliRepoFromURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
			if owner != tc.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tc.wantOwner)
			}
			if repo != tc.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tc.wantRepo)
			}
		})
	}
}

// TestNumericClaimNestedNamespace verifies that a numeric claim against a
// project whose repo origin URL uses a nested GitLab namespace constructs the
// correct merge-request path and round-trips through cliParsePRURL. This is the
// regression scenario from reviewer Item 10: before the fix, cliRepoFromURL
// truncated the namespace to the first component, so a numeric claim built
// /group/subgroup/-/merge_requests/N and failed same-repository validation.
func TestNumericClaimNestedNamespace(t *testing.T) {
	const repoOrigin = "https://gitlab.com/group/subgroup/repo.git"
	host, owner, repo, err := cliRepoFromURL(repoOrigin)
	if err != nil {
		t.Fatalf("cliRepoFromURL: %v", err)
	}
	const num = 7
	mrURL := domain.ChangeRequestURL(host, owner+"/"+repo, num)
	const wantURL = "https://gitlab.com/group/subgroup/repo/-/merge_requests/7"
	if mrURL != wantURL {
		t.Fatalf("mrURL = %q, want %q", mrURL, wantURL)
	}
	// Round-trip: the constructed MR URL must parse back to the same
	// host/owner/repo/number so same-repository validation passes.
	parsed, err := domain.ParsePastedChangeRequestURL(mrURL)
	if err != nil {
		t.Fatalf("ParsePastedChangeRequestURL(%q): %v", mrURL, err)
	}
	if parsed.Host != host || parsed.Repository != owner+"/"+repo || parsed.Number != num {
		t.Errorf("round-trip mismatch: got %+v", parsed)
	}
}

func TestResolvePRRef(t *testing.T) {
	c := &commandContext{}
	project := projectDetails{Repo: "https://github.com/Acme/Repo.git"}
	for _, tc := range []struct {
		name, ref, want string // want "" = usage error
	}{
		{"pasted browser url", "https://www.github.com/Acme/Repo/pull/7/files?w=1#diff-abc", "https://github.com/acme/repo/pull/7"},
		{"numeric matches pasted", "7", "https://github.com/acme/repo/pull/7"},
		{"hash numeric", "#7", "https://github.com/acme/repo/pull/7"},
		{"gitlab mr", "https://gitlab.com/g/s/r/-/merge_requests/9?tab=diffs", "https://gitlab.com/g/s/r/-/merge_requests/9"},
		{"http rejected", "http://github.com/o/r/pull/1", ""},
		{"bad number", "https://github.com/o/r/pull/abc", ""},
		{"gist rejected", "https://gist.github.com/o/r/pull/1", ""},
		{"empty", " ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.resolvePRRef(context.Background(), tc.ref, project)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("got %q, want error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
