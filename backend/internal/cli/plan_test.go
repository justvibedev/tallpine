package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const planJSON = `{"plan":{"id":"plan_1","status":"awaiting_answers","questions":[{"id":"q1","text":"Who pays?","options":[{"id":"a","label":"Each"},{"id":"b","label":"Booker"}],"recommended":"a"}],"assumptions":[{"id":"s1","text":"Assumed: 10 min"}]}}`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPlanQuestions_RejectsSixBeforeTheDaemon(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, planJSON })
	var qs []string
	for i := 0; i < 6; i++ {
		qs = append(qs, fmt.Sprintf(`{"id":"q%d","text":"t","options":[{"id":"a","label":"x"}],"recommended":"a"}`, i))
	}
	file := writeFile(t, "q.json", `{"questions":[`+strings.Join(qs, ",")+`],"assumptions":[]}`)
	_, err := runBacklog(t, "plan", "questions", "B1", "--file", file)
	if err == nil || !strings.Contains(err.Error(), "at most 5 questions") {
		t.Fatalf("err = %v, want the 5-question cap", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("a rejected file reached the daemon: %+v", *calls)
	}
}

func TestPlanQuestionsAndSubmit_SendFilesToThePlanRoutes(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, planJSON })
	q := writeFile(t, "q.json", `{"questions":[{"id":"q1","text":"Who pays?","options":[{"id":"a","label":"Each"}],"recommended":"a"}],"assumptions":[{"id":"s1","text":"Assumed: x"}]}`)
	if _, err := runBacklog(t, "plan", "questions", "B1", "--file", q); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	if last.method != "POST" || last.path != "/api/v1/projects/demo/backlog/B1/plan/questions" || !strings.Contains(last.body, `"recommended":"a"`) {
		t.Fatalf("questions sent %+v", last)
	}
	plan := writeFile(t, "plan.md", "# Plan")
	dec := writeFile(t, "d.md", "| q | a |")
	prs := writeFile(t, "prs.json", `[{"title":"PR1","produces":["X()"]}]`)
	focus := writeFile(t, "f.json", `["harm"]`)
	if _, err := runBacklog(t, "plan", "submit", "B1", "--plan", plan, "--decisions", dec, "--prs", prs, "--focus", focus); err != nil {
		t.Fatal(err)
	}
	last = (*calls)[len(*calls)-1]
	if last.path != "/api/v1/projects/demo/backlog/B1/plan/submit" || !strings.Contains(last.body, `"focus":["harm"]`) {
		t.Fatalf("submit sent %+v", last)
	}
	// An entry without produces or consumes, or six focus items, never leaves the CLI.
	bad := writeFile(t, "bad.json", `[{"title":"PR1"}]`)
	before := len(*calls)
	if _, err := runBacklog(t, "plan", "submit", "B1", "--plan", plan, "--decisions", dec, "--prs", bad); err == nil {
		t.Error("PR entry without produces/consumes accepted")
	}
	six := writeFile(t, "six.json", `["1","2","3","4","5","6"]`)
	if _, err := runBacklog(t, "plan", "submit", "B1", "--plan", plan, "--decisions", dec, "--prs", prs, "--focus", six); err == nil {
		t.Error("six focus items accepted")
	}
	if len(*calls) != before {
		t.Fatal("invalid submissions reached the daemon")
	}
}

func TestPlanApproveAndRelay_SendOwnerMessage(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, planJSON })
	if _, err := runBacklog(t, "plan", "approve", "B1", "--owner-message", "msg-1"); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	if last.path != "/api/v1/projects/demo/backlog/B1/plan/approve" || last.body != `{"ownerMessage":"msg-1"}` {
		t.Fatalf("approve sent %+v", last)
	}
	if _, err := runBacklog(t, "plan", "defaults", "B1", "--relayed"); err != nil {
		t.Fatal(err)
	}
	if last = (*calls)[len(*calls)-1]; last.body != `{"relayed":true}` {
		t.Fatalf("defaults sent %+v", last)
	}
	if _, err := runBacklog(t, "plan", "start", "B1"); err != nil {
		t.Fatal(err)
	}
	if last = (*calls)[len(*calls)-1]; last.method != "POST" || last.path != "/api/v1/projects/demo/backlog/B1/plan" {
		t.Fatalf("start sent %+v", last)
	}
}

func TestPlanShow_PrintsRecommendationAndAssumptions(t *testing.T) {
	backlogFake(t, func(backlogCall) (int, string) { return 200, planJSON })
	out, err := runBacklog(t, "plan", "show", "B1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"plan_1: awaiting_answers", "Question q1: Who pays?", "* a: Each", "Assumption s1: Assumed: 10 min"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
}

func TestSpawnRole_ValidatesBeforeTheDaemon(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--role", "boss"}, `--role must be "planner"`},
		{[]string{"--role", "planner", "--kind", "orchestrator"}, "--role is for project worker sessions"},
		{[]string{"--role", "planner", "--standalone"}, "--role is for project worker sessions"},
	} {
		args := append([]string{"spawn", "--name", "x", "--agent", "codex"}, tc.args...)
		if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
}
