package review

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestRereviewMatchesAcrossCaseAndPastedForms(t *testing.T) {
	prs := []domain.PullRequest{{URL: "https://github.com/Acme/Repo/pull/7", HeadSHA: "ABCDEF1"}}
	for _, want := range []string{"https://github.com/acme/repo/pull/7", "https://github.com/Acme/Repo/pull/7/files?w=1"} {
		if _, ok := SelectRereviewPR(prs, want); !ok {
			t.Errorf("SelectRereviewPR(%q) missed the stored row", want)
		}
		if got, err := CheckExpectedHead(prs, want, "abcdef"); err != nil || got == "" {
			t.Errorf("CheckExpectedHead(%q) = %q, %v", want, got, err)
		}
	}
}
