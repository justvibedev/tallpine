package domain

import "testing"

func TestPRStateTable(t *testing.T) {
	cases := []struct {
		merged, closed, draft bool
		want                  PRState
	}{
		{false, false, false, PRStateOpen},
		{false, false, true, PRStateDraft},
		{false, true, true, PRStateClosed},
		{true, true, true, PRStateMerged},
		{true, false, false, PRStateMerged},
	}
	for _, c := range cases {
		if got := (PullRequest{Merged: c.merged, Closed: c.closed, Draft: c.draft}).State(); got != c.want {
			t.Errorf("PullRequest %+v = %s want %s", c, got, c.want)
		}
		if got := (PRFacts{Merged: c.merged, Closed: c.closed, Draft: c.draft}).State(); got != c.want {
			t.Errorf("PRFacts %+v = %s want %s", c, got, c.want)
		}
	}
}
