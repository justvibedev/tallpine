package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func askOpts() []PlanOption { return []PlanOption{{ID: "a", Label: "Yes"}, {ID: "b", Label: "No"}} }

func TestNormalizeAskText(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"Use Postgres?", "  use   postgres "},
		{"Ship B62, now!", "ship b62 now"},
	} {
		if NormalizeAskText(c.a) != NormalizeAskText(c.b) {
			t.Errorf("%q and %q normalize differently: %q vs %q", c.a, c.b, NormalizeAskText(c.a), NormalizeAskText(c.b))
		}
	}
	if NormalizeAskText("Use Postgres") == NormalizeAskText("Use MySQL") {
		t.Error("different questions normalized alike")
	}
}

func TestOwnerAskMatches(t *testing.T) {
	a := OwnerAsk{Text: "Use Postgres?", About: "B62"}
	if !a.Matches("b62", "unrelated") || !a.Matches("", "use postgres") {
		t.Error("about key or normalized text should match")
	}
	if a.Matches("", "Use MySQL?") || (OwnerAsk{Text: "x"}).Matches("", "y") {
		t.Error("unrelated matched")
	}
}

func TestValidateAskCaps(t *testing.T) {
	long := strings.Repeat("x", MaxAskTextChars+1)
	seven := append(askOpts(), PlanOption{ID: "c", Label: "c"}, PlanOption{ID: "d", Label: "d"}, PlanOption{ID: "e", Label: "e"}, PlanOption{ID: "f", Label: "f"}, PlanOption{ID: "g", Label: "g"})
	for name, err := range map[string]error{
		"empty text":     ValidateAsk(" ", "", "", askOpts(), "a", ""),
		"long text":      ValidateAsk(long, "", "", askOpts(), "a", ""),
		"one option":     ValidateAsk("q", "", "", askOpts()[:1], "a", ""),
		"seven options":  ValidateAsk("q", "", "", seven, "a", ""),
		"dup option":     ValidateAsk("q", "", "", []PlanOption{{ID: "a", Label: "x"}, {ID: "a", Label: "y"}}, "a", ""),
		"bad recommend":  ValidateAsk("q", "", "", askOpts(), "z", ""),
		"long about":     ValidateAsk("q", "", strings.Repeat("a", MaxAskAboutChars+1), askOpts(), "a", ""),
		"long reask":     ValidateAsk("q", "", "", askOpts(), "a", strings.Repeat("a", MaxAskReasonChars+1)),
		"unlabelled opt": ValidateAsk("q", "", "", []PlanOption{{ID: "a"}, {ID: "b", Label: "y"}}, "a", ""),
	} {
		if err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := ValidateAsk("q", "why", "B1", askOpts(), "a", "changed"); err != nil {
		t.Errorf("valid ask refused: %v", err)
	}
	six := append(askOpts(), PlanOption{ID: "c", Label: "c"}, PlanOption{ID: "d", Label: "d"}, PlanOption{ID: "e", Label: "e"}, PlanOption{ID: "f", Label: "f"})
	if err := ValidateAsk("q", "", "", six, "f", ""); err != nil {
		t.Errorf("six options refused: %v", err)
	}
}

func TestOwnerAskTransitions(t *testing.T) {
	now := time.Now()
	a := OwnerAsk{Ref: "Q1", State: AskOpen, Options: askOpts()}
	ans, err := a.ResolveAskAnswer("b", "")
	if err != nil || ans.Text != "No" || ans.OptionID != "b" {
		t.Fatalf("resolve = %+v %v", ans, err)
	}
	done, err := a.Answered(ans, now)
	if err != nil || done.State != AskAnswered || done.Answer.Text != "No" || !done.AnsweredAt.Equal(now) {
		t.Fatalf("answered = %+v %v", done, err)
	}
	if _, err := done.Answered(ans, now); !errors.Is(err, ErrAskNotOpen) {
		t.Errorf("second answer: %v", err)
	}
	if _, err := done.Withdrawn("x", now); !errors.Is(err, ErrAskNotOpen) {
		t.Errorf("withdraw answered: %v", err)
	}
	w, err := a.Withdrawn("moot", now)
	if err != nil || w.State != AskWithdrawn || w.WithdrawReason != "moot" {
		t.Fatalf("withdrawn = %+v %v", w, err)
	}
	if _, err := w.Answered(ans, now); !errors.Is(err, ErrAskNotOpen) {
		t.Errorf("answer withdrawn: %v", err)
	}
	if a.State != AskOpen {
		t.Error("transition mutated the receiver")
	}
}

func TestResolveAskAnswerRejects(t *testing.T) {
	a := OwnerAsk{Ref: "Q1", Options: askOpts()}
	for _, c := range [][2]string{{"", ""}, {"a", "x"}, {"zz", ""}} {
		if _, err := a.ResolveAskAnswer(c[0], c[1]); err == nil {
			t.Errorf("%v accepted", c)
		}
	}
	if ans, err := a.ResolveAskAnswer("", "free"); err != nil || ans.Other != "free" || ans.OptionID != "" {
		t.Errorf("other = %+v %v", ans, err)
	}
}
