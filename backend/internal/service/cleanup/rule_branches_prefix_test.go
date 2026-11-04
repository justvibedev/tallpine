package cleanup

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestSessionOfBranchAcceptsBothBranchPrefixes(t *testing.T) {
	sessions := map[domain.SessionID]domain.SessionRecord{"mer-1": {ID: "mer-1"}}
	for _, branch := range []string{"vo/mer-1/root", "tp/mer-1/root", "tp/dev/mer-1/root"} {
		if rec, ok := sessionOfBranch(branch, sessions); !ok || rec.ID != "mer-1" {
			t.Errorf("sessionOfBranch(%q) = %v, %v; want mer-1", branch, rec.ID, ok)
		}
	}
	for _, branch := range []string{"ao/mer-1/root", "xx/mer-1/root"} {
		if _, ok := sessionOfBranch(branch, sessions); ok {
			t.Errorf("sessionOfBranch(%q) matched; only vo/ and tp/ are cleanup-owned", branch)
		}
	}
}
