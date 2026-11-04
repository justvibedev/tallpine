package session

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/observe/scm"
)

func TestSessionOwnsBranchForClaim(t *testing.T) {
	mk := func(branch, temp string) domain.SessionRecord {
		r := domain.SessionRecord{ID: "a-1", TemporaryBranch: temp}
		r.Metadata.Branch = branch
		return r
	}
	cases := []struct {
		name      string
		sess      domain.SessionRecord
		workspace bool
		head      string
		want      bool
	}{
		{"exact", mk("tp/a-1/root", ""), false, "tp/a-1/root", true},
		{"root namespace sibling", mk("tp/a-1/root", ""), false, "tp/a-1/topic", true},
		{"other session", mk("tp/a-1/root", ""), false, "tp/a-2/topic", false},
		{"hyphen topic refused", mk("feature/login", ""), false, "feature/login-v2", false},
		{"other session id extension", mk("tp/a-1", ""), false, "tp/a-10", false},
		{"main", mk("tp/a-1", ""), false, "main", false},
		{"renamed task keeps temporary namespace", mk("tp/fix-login/root", "tp/a-1/root"), false, "tp/a-1/follow-up", true},
		{"renamed task, foreign namespace", mk("tp/fix-login/root", "tp/a-1/root"), false, "tp/a-2/follow-up", false},
		{"workspace numeric hyphen", mk("tp/a-1", ""), true, "tp/a-1-2", true},
	}
	for _, c := range cases {
		if got := scm.SessionOwnsBranch(c.sess, c.workspace, c.head); got != c.want {
			t.Errorf("%s: = %v, want %v", c.name, got, c.want)
		}
	}
}
