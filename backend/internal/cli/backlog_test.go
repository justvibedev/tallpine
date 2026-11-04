package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type backlogCall struct {
	method, path, query, body string
}

// backlogFake answers the project lookup and records every backlog request;
// reply picks the JSON body for a backlog call.
func backlogFake(t *testing.T, reply func(c backlogCall) (int, string)) *[]backlogCall {
	t.Helper()
	cfg := setConfigEnv(t)
	t.Setenv("TALLPINE_PROJECT_ID", "demo")
	t.Setenv("TALLPINE_SESSION_ID", "")
	var calls []backlogCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/projects/demo" {
			_, _ = io.WriteString(w, `{"project":{"id":"demo","kind":"single_repo"}}`)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		c := backlogCall{r.Method, r.URL.Path, r.URL.RawQuery, strings.TrimSpace(string(raw))}
		calls = append(calls, c)
		status, body := reply(c)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	return &calls
}

func runBacklog(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
	return out, err
}

const (
	itemB1 = `{"id":"bl_1","ref":"B1","projectId":"demo","title":"Big feature","note":"Body **md**","doneWhen":"ships","state":"open","hold":"","scope":"pr","childCount":1,"origin":{"kind":"human"},"agentNotes":"look at x.go","fields":{"area":"cli","branch":"feat/x"},"links":[{"kind":"pr","ref":"https://github.com/o/r/pull/1","display":"https://github.com/o/r/pull/1"},{"kind":"blocked_by","ref":"bl_3","display":"B3"}],"linkCounts":{}}`
	itemB2 = `{"id":"bl_2","ref":"B2","projectId":"demo","title":"Part one","state":"inbox","hold":"after rename","parentRef":"B1","origin":{"kind":"worker","sessionId":"app-7"},"fields":{},"links":[],"linkCounts":{"session":1}}`
)

func TestBacklogLsGoldenOutput(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, `{"items":[` + itemB1 + `,` + itemB2 + `]}` })
	out, err := runBacklog(t, "backlog", "ls", "--state", "open", "--unheld", "--q", "part")
	if err != nil {
		t.Fatal(err)
	}
	want := "REF  STATE  PARENT  HOLD          TITLE\n" +
		"B1   open   -       -             Big feature\n" +
		"B2   inbox  B1      after rename  Part one\n"
	if out != want {
		t.Fatalf("ls output:\n%q\nwant:\n%q", out, want)
	}
	c := (*calls)[0]
	if c.method != "GET" || c.path != "/api/v1/projects/demo/backlog" || c.query != "held=unheld&q=part&state=open" {
		t.Fatalf("request = %+v", c)
	}
	jsonOut, err := runBacklog(t, "backlog", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct{ Items []map[string]any }
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil || len(parsed.Items) != 2 || parsed.Items[1]["ref"] != "B2" {
		t.Fatalf("json = %q err=%v", jsonOut, err)
	}
}

func TestBacklogShowGoldenOutput(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, `{"item":` + itemB1 + `}` })
	out, err := runBacklog(t, "backlog", "show", "B1")
	if err != nil {
		t.Fatal(err)
	}
	want := `ref:       B1
title:     Big feature
state:     open
scope:     pr
children:  1
done when: ships
origin:    human
fields:
  area=cli
  branch=feat/x
links:
  pr https://github.com/o/r/pull/1
  blocked_by B3

Body **md**

agent notes:
look at x.go
`
	if out != want {
		t.Fatalf("show output:\n%s\nwant:\n%s", out, want)
	}
	if c := (*calls)[0]; c.method != "GET" || c.path != "/api/v1/projects/demo/backlog/B1" {
		t.Fatalf("request = %+v", c)
	}
}

func TestBacklogNextOutput(t *testing.T) {
	reply := `{"item":null}`
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, reply })
	out, err := runBacklog(t, "backlog", "next", "--parent", "B1")
	if err != nil || out != "nothing is ready: no open, unheld, unblocked item\n" {
		t.Fatalf("empty next = %q %v", out, err)
	}
	if c := (*calls)[0]; c.path != "/api/v1/projects/demo/backlog/next" || c.query != "parent=B1" {
		t.Fatalf("request = %+v", c)
	}
	reply = `{"item":` + itemB2 + `}`
	out, err = runBacklog(t, "backlog", "next")
	if err != nil || !strings.HasPrefix(out, "ref:       B2\n") {
		t.Fatalf("next = %q %v", out, err)
	}
}

func TestBacklogAddReadsTitleAndNoteFromStdin(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 201, `{"item":` + itemB2 + `}` })
	var out strings.Builder
	cmd := NewRootCommand(Deps{ProcessAlive: func(int) bool { return true }, Out: &out, Err: io.Discard})
	cmd.SetIn(strings.NewReader("Fix `the` thing\nsecond line\nthird\n"))
	cmd.SetArgs([]string{"backlog", "add", "-", "--parent", "B1", "--open"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte((*calls)[0].body), &body)
	if body["title"] != "Fix `the` thing" || body["note"] != "second line\nthird" || body["parent"] != "B1" || body["open"] != true {
		t.Fatalf("body = %v", body)
	}
	if out.String() != "added B2 [inbox] Part one\n" {
		t.Fatalf("output = %q", out.String())
	}
	if _, err := runBacklog(t, "backlog", "add", " "); err == nil {
		t.Fatal("blank title accepted")
	}
}

func TestBacklogEditSendsOnlyChangedFlags(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 200, `{"item":` + itemB1 + `}` })
	if _, err := runBacklog(t, "backlog", "edit", "B1", "--title", "New", "--done-when", "x", "--no-parent"); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.method != "PATCH" || c.path != "/api/v1/projects/demo/backlog/B1" || c.body != `{"doneWhen":"x","parent":"","title":"New"}` {
		t.Fatalf("request = %+v", c)
	}
	if _, err := runBacklog(t, "backlog", "edit", "B1"); err == nil {
		t.Fatal("edit with nothing to change accepted")
	}
	var usage usageError
	if _, err := runBacklog(t, "backlog", "edit", "B1", "--parent", "B2", "--no-parent"); !errors.As(err, &usage) {
		t.Fatalf("conflicting parent flags err = %v", err)
	}
}

func TestBacklogSetLinkHoldStateAndMove(t *testing.T) {
	calls := backlogFake(t, func(c backlogCall) (int, string) {
		if strings.HasSuffix(c.path, "/reorder") {
			return 204, ""
		}
		return 200, `{"item":` + itemB1 + `}`
	})
	steps := [][]string{
		{"backlog", "set", "B1", "area=cli", "note="},
		{"backlog", "link", "B1", "blocked-by:B3"},
		{"backlog", "unlink", "B1", "pr:https://github.com/o/r/pull/1"},
		{"backlog", "hold", "B1", "after", "the", "rename"},
		{"backlog", "unhold", "B1"},
		{"backlog", "done", "B1", "--reason", "shipped"},
		{"backlog", "drop", "B1"},
		{"backlog", "accept", "B1"},
		{"backlog", "move", "B1", "--after", "B4"},
	}
	for _, args := range steps {
		if _, err := runBacklog(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	want := []backlogCall{
		{"PUT", "/api/v1/projects/demo/backlog/B1/fields/area", "", `{"value":"cli"}`},
		{"PUT", "/api/v1/projects/demo/backlog/B1/fields/note", "", `{"value":""}`},
		{"POST", "/api/v1/projects/demo/backlog/B1/links", "", `{"kind":"blocked_by","ref":"B3"}`},
		{"DELETE", "/api/v1/projects/demo/backlog/B1/links", "", `{"kind":"pr","ref":"https://github.com/o/r/pull/1"}`},
		{"PATCH", "/api/v1/projects/demo/backlog/B1", "", `{"hold":"after the rename"}`},
		{"PATCH", "/api/v1/projects/demo/backlog/B1", "", `{"hold":""}`},
		{"POST", "/api/v1/projects/demo/backlog/B1/state", "", `{"state":"done","reason":"shipped"}`},
		{"POST", "/api/v1/projects/demo/backlog/B1/state", "", `{"state":"dropped"}`},
		{"POST", "/api/v1/projects/demo/backlog/B1/state", "", `{"state":"open"}`},
		{"POST", "/api/v1/projects/demo/backlog/reorder", "", `{"ref":"B1","after":"B4"}`},
	}
	if len(*calls) != len(want) {
		t.Fatalf("calls = %+v", *calls)
	}
	for i, w := range want {
		if (*calls)[i] != w {
			t.Errorf("call %d = %+v, want %+v", i, (*calls)[i], w)
		}
	}
	var usage usageError
	if _, err := runBacklog(t, "backlog", "move", "B1"); !errors.As(err, &usage) {
		t.Fatalf("move without target err = %v", err)
	}
	if _, err := runBacklog(t, "backlog", "link", "B1", "nocolon"); !errors.As(err, &usage) {
		t.Fatalf("bad link spec err = %v", err)
	}
}

func TestReportDeferSendsLinesAndPrintsOutcomes(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "worker-1")
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"r1","deferred":[`+
			`{"line":"one","status":"filed","ref":"B4"},`+
			`{"line":"two","status":"merged","ref":"B2"},`+
			`{"line":"three","status":"inbox_full","reason":"not filed: inbox cap reached"}]}`)
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	out, err := runBacklog(t, "report", "--checkpoint", "--note", "halfway", "--defer", " one ", "--defer", "two", "--defer", "three")
	if err != nil {
		t.Fatal(err)
	}
	if lines, _ := body["defer"].([]any); len(lines) != 3 || lines[0] != "one" {
		t.Fatalf("request defer = %v", body["defer"])
	}
	want := "report sent: state=checkpoint session=worker-1 id=r1 (delivered to the project orchestrator)\ndeferred as B4: one\ndeferred (already tracked as B2): two\nnot filed: inbox cap reached: three\n"
	if out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

func TestReportDeferLimitsAreUsageErrors(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("TALLPINE_SESSION_ID", "worker-1")
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	long := strings.Repeat("x", 201)
	cases := map[string][]string{
		"four lines":                {"report", "hi", "--defer", "a", "--defer", "b", "--defer", "c", "--defer", "d"},
		"empty line":                {"report", "hi", "--defer", "  "},
		"201 characters":            {"report", "hi", "--defer", long},
		"defer alone":               {"report", "--defer", "a"},
		"defer with outputs only":   {"report", "--artifact", "x", "--defer", "a"},
		"defer with state, no note": {"report", "--done", "--defer", "a"},
	}
	for name, args := range cases {
		var usage usageError
		if _, err := runBacklog(t, args...); !errors.As(err, &usage) {
			t.Errorf("%s: err = %v, want usage error", name, err)
		}
	}
	if hits != 0 {
		t.Fatalf("daemon was called %d times for invalid --defer input", hits)
	}
	// Three 200-character lines are within the limits.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"r"}`)
	}))
	t.Cleanup(server2.Close)
	writeRunFileFor(t, cfg, server2)
	atLimit := strings.Repeat("y", 200)
	if _, err := runBacklog(t, "report", "hi", "--defer", atLimit, "--defer", atLimit, "--defer", atLimit); err != nil {
		t.Fatalf("at-limit defer rejected: %v", err)
	}
}

func TestWriteBacklogItemShowsWhyAnOpenItemWithAMergedPRStaysOpen(t *testing.T) {
	var buf bytes.Buffer
	if err := writeBacklogItem(&buf, apitypes.BacklogItemResponse{Ref: "B1", State: "open", AutoClose: "stays open: scope is local"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "auto-close: stays open: scope is local") {
		t.Fatalf("output = %q", buf.String())
	}
}

func TestBacklogLsDefaultsToActive(t *testing.T) {
	for args, want := range map[string]string{"": "state=active", "all": "state=all", "done": "state=done"} {
		calls := backlogFake(t, func(backlogCall) (int, string) { return 200, `{"items":[]}` })
		a := []string{"backlog", "ls"}
		if args != "" {
			a = append(a, "--state", args)
		}
		if _, err := runBacklog(t, a...); err != nil {
			t.Fatal(err)
		}
		if got := (*calls)[0].query; got != want {
			t.Fatalf("--state %q: query = %q, want %q", args, got, want)
		}
	}
}

func TestReportConfirmationNamesOutputsAndRoute(t *testing.T) {
	for _, tt := range []struct{ name, resp, want string }{
		{"pr", `{"id":"r2"}`, "report sent: state=done session=worker-1 id=r2 pr_created=https://github.com/o/r/pull/1 (delivered to the project orchestrator)\n"},
		{"notice", `{"id":"r3","notice":"project X has no orchestrator: reports go to the Inbox"}`, "report sent: state=done session=worker-1 id=r3 pr_created=https://github.com/o/r/pull/1\nnote: project X has no orchestrator: reports go to the Inbox\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("TALLPINE_SESSION_ID", "worker-1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, tt.resp)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			out, err := runBacklog(t, "report", "--done", "--note", "x", "--pr-created", "https://github.com/o/r/pull/1")
			if err != nil || out != tt.want {
				t.Fatalf("out = %q err = %v, want %q", out, err, tt.want)
			}
		})
	}
}
