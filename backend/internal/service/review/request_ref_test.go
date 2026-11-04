package review

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestReviewRequestRefFallsBackToURL(t *testing.T) {
	ref, err := reviewRequestRef(domain.PullRequest{URL: "https://github.com/Acme/Repo/pull/7", Number: 7})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Repo.Provider != "github" || ref.Repo.Host != "github.com" || ref.Repo.Owner != "acme" || ref.Repo.Name != "repo" || ref.Repo.Repo != "acme/repo" {
		t.Fatalf("fallback ref = %+v", ref.Repo)
	}
	// stored fields win over the URL
	ref, err = reviewRequestRef(domain.PullRequest{URL: "https://github.com/a/b/pull/7", Number: 7, Provider: "github", Host: "github.com", Repo: "Keep/Case"})
	if err != nil || ref.Repo.Owner != "Keep" || ref.Repo.Name != "Case" {
		t.Fatalf("stored fields lost: %+v, %v", ref.Repo, err)
	}
	if _, err := reviewRequestRef(domain.PullRequest{URL: "https://example.com/x", Number: 7}); err == nil {
		t.Fatal("unparseable URL with no stored identity must error")
	}
}
