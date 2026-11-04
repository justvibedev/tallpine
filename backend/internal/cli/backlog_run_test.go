package cli

import (
	"io"
	"strings"
	"testing"
)

const runJSON = `{"run":{"ref":"R1","scope":"pr","selection":"all","refs":[],"status":"running","items":[` +
	`{"ref":"B1","title":"First","lane":0,"position":0,"model":"opus","status":"active","displayStatus":"active","usage":{"costComplete":true}},` +
	`{"ref":"B2","title":"Second","lane":1,"position":0,"model":"sonnet","status":"needs_you","displayStatus":"needs_you","question":"Which API?","usage":{"costNanos":2500000000,"costComplete":false}}],` +
	`"counts":{"active":1,"needs_you":1},"usage":{"costNanos":2500000000,"costComplete":false}}}`

func TestBacklogRunStartSelectionsAndScope(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 201, runJSON })
	cases := []struct {
		args []string
		body string
	}{
		{[]string{"--scope", "pr", "--all"}, `{"scope":"pr","selection":"all"}`},
		{[]string{"--scope", "local", "B1", "B2"}, `{"scope":"local","selection":"refs","refs":["B1","B2"]}`},
		{[]string{"--scope", "pr-merge", "--parent", "B30"}, `{"scope":"pr-merge","selection":"parent","refs":["B30"]}`},
	}
	for _, c := range cases {
		if _, err := runBacklog(t, append([]string{"backlog", "run", "start"}, c.args...)...); err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		last := (*calls)[len(*calls)-1]
		if last.method != "POST" || last.path != "/api/v1/projects/demo/backlog-runs" || last.body != c.body {
			t.Fatalf("%v sent %+v, want POST backlog-runs %s", c.args, last, c.body)
		}
	}
	before := len(*calls)
	for _, args := range [][]string{{"--all"}, {"--scope", "pr"}, {"--scope", "pr", "--all", "B1"}, {"--scope", "pr", "--all", "--parent", "B1"}} {
		if _, err := runBacklog(t, append([]string{"backlog", "run", "start"}, args...)...); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
	if len(*calls) != before {
		t.Fatal("invalid start arguments reached the daemon")
	}
}

func TestBacklogRunStatusDefaultsToTheOpenRunAndRenders(t *testing.T) {
	calls := backlogFake(t, func(c backlogCall) (int, string) {
		if c.path == "/api/v1/projects/demo/backlog-runs" {
			return 200, `{"runs":[{"ref":"R1"}]}`
		}
		return 200, runJSON
	})
	out, err := runBacklog(t, "backlog", "run", "status")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"R1 [running] scope pr, all", "lane 1", "lane 2", "B2 needs_you", "? Which API?", "cost $2.50 (partial"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	if got := (*calls)[0]; got.query != "open=1" {
		t.Fatalf("first call = %+v, want the open-run lookup", got)
	}
}

func TestBacklogRunPlanItemPauseFinishRoutes(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, runJSON })
	plan := `{"lanes":[[{"ref":"B1","model":"opus"}]]}`
	planCmd := NewRootCommand(Deps{ProcessAlive: func(int) bool { return true }, Out: io.Discard, Err: io.Discard})
	planCmd.SetIn(strings.NewReader(plan))
	planCmd.SetArgs([]string{"backlog", "run", "plan", "R1", "--file", "-"})
	if err := planCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		args         []string
		method, path string
	}{
		{[]string{"backlog", "run", "item", "R1", "B2", "--status", "needs_you", "--question", "Which API?"}, "POST", "/api/v1/projects/demo/backlog-runs/R1/items/B2"},
		{[]string{"backlog", "run", "pause", "R1", "--reason", "usage limit", "--resume-at", "2026-10-28T15:00:00Z"}, "POST", "/api/v1/projects/demo/backlog-runs/R1/pause"},
		{[]string{"backlog", "run", "resume", "R1"}, "POST", "/api/v1/projects/demo/backlog-runs/R1/resume"},
		{[]string{"backlog", "run", "stop", "R1"}, "POST", "/api/v1/projects/demo/backlog-runs/R1/stop"},
		{[]string{"backlog", "run", "finish", "R1", "--summary", "B1 landed"}, "POST", "/api/v1/projects/demo/backlog-runs/R1/finish"},
	}
	if got := (*calls)[0]; got.method != "PUT" || got.path != "/api/v1/projects/demo/backlog-runs/R1/plan" || got.body != plan {
		t.Fatalf("plan call = %+v", got)
	}
	for _, s := range steps {
		if _, err := runBacklog(t, s.args...); err != nil {
			t.Fatalf("%v: %v", s.args, err)
		}
		if got := (*calls)[len(*calls)-1]; got.method != s.method || got.path != s.path {
			t.Fatalf("%v called %s %s, want %s %s", s.args, got.method, got.path, s.method, s.path)
		}
	}
	pause := (*calls)[2]
	if !strings.Contains(pause.body, `"reason":"usage limit"`) || !strings.Contains(pause.body, `"resumeAt":"2026-10-28T15:00:00Z"`) {
		t.Fatalf("pause body = %s", pause.body)
	}
	if _, err := runBacklog(t, "backlog", "run", "pause", "R1", "--resume-at", "tomorrow"); err == nil {
		t.Fatal("a non-RFC3339 --resume-at was accepted")
	}
}
