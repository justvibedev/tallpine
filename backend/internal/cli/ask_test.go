package cli

import (
	"strings"
	"testing"
)

const askJSON = `{"ask":{"id":"a1","projectId":"demo","ref":"Q3","text":"Ship B62?","options":[{"id":"y","label":"Yes"},{"id":"n","label":"No"}],"recommended":"y","state":"open","anchored":false,"createdAt":"2026-11-13T12:00:00Z","updatedAt":"2026-11-13T12:00:00Z"}}`

func TestAsk_SendsTheQuestionToTheAsksRoute(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 201, askJSON })
	out, err := runBacklog(t, "ask", "Ship B62?", "--option", "y=Yes", "--option", "n=No, wait", "--recommended", "y", "--about", "B62", "--reask", "scope changed")
	if err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if c.method != "POST" || c.path != "/api/v1/projects/demo/asks" {
		t.Fatalf("request = %+v", c)
	}
	for _, want := range []string{`"text":"Ship B62?"`, `"label":"No, wait"`, `"recommended":"y"`, `"about":"B62"`, `"reask":"scope changed"`} {
		if !strings.Contains(c.body, want) {
			t.Errorf("body %s lacks %s", c.body, want)
		}
	}
	if !strings.Contains(out, "Q3 [open] Ship B62?") {
		t.Fatalf("out = %q", out)
	}
}

func TestAsk_FlagValidationNeverReachesTheDaemon(t *testing.T) {
	calls := backlogFake(t, func(backlogCall) (int, string) { return 201, askJSON })
	long := strings.Repeat("x", 501)
	for name, args := range map[string][]string{
		"no options":        {"ask", "Q?", "--recommended", "y"},
		"one option":        {"ask", "Q?", "--option", "y=Yes", "--recommended", "y"},
		"seven options":     {"ask", "Q?", "--option", "a=1", "--option", "b=2", "--option", "c=3", "--option", "d=4", "--option", "e=5", "--option", "f=6", "--option", "g=7", "--recommended", "a"},
		"malformed option":  {"ask", "Q?", "--option", "yes", "--option", "n=No", "--recommended", "n"},
		"bad recommended":   {"ask", "Q?", "--option", "y=Yes", "--option", "n=No", "--recommended", "z"},
		"missing recommend": {"ask", "Q?", "--option", "y=Yes", "--option", "n=No"},
		"long text":         {"ask", long, "--option", "y=Yes", "--option", "n=No", "--recommended", "y"},
	} {
		if _, err := runBacklog(t, args...); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("invalid asks reached the daemon: %+v", *calls)
	}
}

// A worker's request is refused by the daemon (scope gate and service); the CLI
// surfaces that refusal instead of swallowing it.
func TestAsk_WorkerRefusalIsShown(t *testing.T) {
	backlogFake(t, func(backlogCall) (int, string) {
		return 403, `{"error":"forbidden","code":"ASK_ORCHESTRATOR_ONLY","message":"Only an orchestrator asks the owner; a worker reports to its orchestrator"}`
	})
	_, err := runBacklog(t, "ask", "Q?", "--option", "y=Yes", "--option", "n=No", "--recommended", "y")
	if err == nil || !strings.Contains(err.Error(), "Only an orchestrator asks the owner") {
		t.Fatalf("err = %v", err)
	}
}

func TestAskLsAnswerWithdraw(t *testing.T) {
	calls := backlogFake(t, func(c backlogCall) (int, string) {
		if c.method == "GET" {
			return 200, `{"asks":[` + strings.TrimSuffix(strings.TrimPrefix(askJSON, `{"ask":`), "}") + `]}`
		}
		return 200, askJSON
	})
	out, err := runBacklog(t, "ask", "ls", "--all")
	if err != nil || !strings.Contains(out, "Q3 [open] Ship B62?") {
		t.Fatalf("ls = %q %v", out, err)
	}
	if c := (*calls)[0]; c.path != "/api/v1/projects/demo/asks" || c.query != "state=all" {
		t.Fatalf("ls request = %+v", c)
	}
	if _, err := runBacklog(t, "ask", "answer", "Q3", "--option", "y", "--owner-message", "last"); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[1]; c.path != "/api/v1/projects/demo/asks/Q3/answer" || !strings.Contains(c.body, `"ownerMessage":"last"`) || !strings.Contains(c.body, `"option":"y"`) {
		t.Fatalf("answer request = %+v", c)
	}
	if _, err := runBacklog(t, "ask", "withdraw", "Q3", "--reason", "moot"); err != nil {
		t.Fatal(err)
	}
	if c := (*calls)[2]; c.path != "/api/v1/projects/demo/asks/Q3/withdraw" || !strings.Contains(c.body, `"reason":"moot"`) {
		t.Fatalf("withdraw request = %+v", c)
	}
	n := len(*calls)
	for name, args := range map[string][]string{
		"both option and text": {"ask", "answer", "Q3", "--option", "y", "--text", "x", "--owner-message", "last"},
		"neither":              {"ask", "answer", "Q3", "--owner-message", "last"},
		"no owner message":     {"ask", "answer", "Q3", "--option", "y"},
	} {
		if _, err := runBacklog(t, args...); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if len(*calls) != n {
		t.Fatal("invalid answers reached the daemon")
	}
}
