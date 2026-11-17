package domain

import "testing"

// The web-5 reply (B750) ended a full result on "Want me to land it?" with no
// marker and no ask; plain status must not count. The frontend mirror
// (asksOwnerAtEnd in chat/WorkerUpdates.test.ts) runs the same cases.
func TestAsksOwner(t *testing.T) {
	web5 := "web-6 finished the checkout fix.\n\n- PR #41 is green, review approved\n- Tests: 212 passed\n\nWant me to land it?"
	cases := map[string]struct {
		text string
		want bool
	}{
		"web-5 offer to land":         {web5, true},
		"question then link line":     {"Done.\n\nShall we ship 0.4 now?\n\nPR: https://github.com/x/y/pull/4", true},
		"offer without question mark": {"PR #12 is green.\n\nSay \"merge\" and I land it.", true},
		"let me know":                 {"Two options drafted.\n\nLet me know which one you prefer.", true},
		"needs you marker":            {"**Needs you:** install the build and test login.", true},
		"plain status":                {"web-6 finished; PR #41 merged. Nothing waiting on you.", false},
		"early question, status end":  {"Was the flake real? Yes, fixed.\n\nworker 7 is on it.\n\nPR #9 merged.\n\nAll green.", false},
		"question in code":            {"Ran `a?.b` checks.\n\n```\nok?\n```\n\nAll done.", false},
		"word inside a word":          {"Fixed the retry.\n\nYour callback now fires once.", false},
		"question in link":            {"See [notes](https://x.y/?q=1) for details.", false},
	}
	for name, c := range cases {
		if got := AsksOwner(c.text); got != c.want {
			t.Errorf("%s: AsksOwner = %v, want %v", name, got, c.want)
		}
	}
}
