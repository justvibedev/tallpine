package cli

import (
	"strings"
	"testing"
)

func TestBrowserHandoffSendsReasonGlobAndTimeout(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{result: `{"outcome":"url-matched","url":"https://app.example/home"}`}
	srv := browserCLIServer(t, capture)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}

	out, _, err := executeCLI(t, deps, "browser", "handoff", "--reason", "  Enter the 2FA code  ",
		"--until-url", "https://app.example/*", "--timeout", "2m")
	if err != nil {
		t.Fatal(err)
	}
	args := capture.body.Args
	if capture.body.Action != "handoff" || args["reason"] != "Enter the 2FA code" ||
		args["untilUrl"] != "https://app.example/*" || args["timeoutMs"] != float64(120_000) {
		t.Fatalf("handoff sent %#v", capture.body)
	}
	if !strings.Contains(out, "the page reached --until-url") || !strings.Contains(out, "Page: https://app.example/home") {
		t.Fatalf("handoff output = %q", out)
	}

	capture.result = `{"outcome":"continued","url":"https://app.example/login"}`
	capture.body.Args = nil
	out, _, err = executeCLI(t, deps, "browser", "handoff", "--reason", "Solve the CAPTCHA")
	if err != nil {
		t.Fatal(err)
	}
	if _, has := capture.body.Args["untilUrl"]; has || capture.body.Args["timeoutMs"] != float64(600_000) {
		t.Fatalf("default handoff sent %#v", capture.body.Args)
	}
	if !strings.Contains(out, "the person clicked Continue") {
		t.Fatalf("continue output = %q", out)
	}
}

func TestBrowserHandoffRejectsInvalidFlagsLocally(t *testing.T) {
	setBrowserIdentity(t)
	cfg := setConfigEnv(t)
	capture := &browserRequestCapture{result: `{}`}
	srv := browserCLIServer(t, capture)
	writeRunFileFor(t, cfg, srv)
	deps := Deps{ProcessAlive: func(int) bool { return true }}
	for _, args := range [][]string{
		{"handoff"},
		{"handoff", "--reason", "   "},
		{"handoff", "--reason", strings.Repeat("x", 501)},
		{"handoff", "--reason", "x", "--timeout", "9s"},
		{"handoff", "--reason", "x", "--timeout", "31m"},
		{"handoff", "--reason", "x", "--until-url", strings.Repeat("u", 2049)},
		{"handoff", "--reason", "x", "extra"},
	} {
		if _, _, err := executeCLI(t, deps, append([]string{"browser"}, args...)...); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
	if capture.body.Action != "" {
		t.Fatalf("an invalid handoff reached the daemon: %#v", capture.body)
	}
}
