package domain

import "testing"

func TestProjectConfigValidatesGitHubUser(t *testing.T) {
	for _, ok := range []string{"", "octocat", "acme-bot", "a1"} {
		if err := (ProjectConfig{GitHubUser: ok}).Validate(); err != nil {
			t.Errorf("githubUser %q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"-lead", "has space", "semi;colon", "a/b", "x@y"} {
		if err := (ProjectConfig{GitHubUser: bad}).Validate(); err == nil {
			t.Errorf("githubUser %q accepted", bad)
		}
	}
}
