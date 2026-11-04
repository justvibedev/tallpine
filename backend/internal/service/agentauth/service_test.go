package agentauth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/service/shellterm"
)

func TestStartRejectsUnstartablePlans(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	svc := New(foundExecutables(nil), opener)

	cases := []struct {
		name    string
		agentID string
		code    string
	}{
		{name: "unknown target", agentID: "not-a-harness", code: "AGENT_AUTH_TARGET_UNKNOWN"},
		{name: "unavailable command", agentID: "codex", code: "AGENT_AUTH_UNAVAILABLE"},
		{name: "removed harness", agentID: "aider", code: "AGENT_AUTH_TARGET_UNKNOWN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Start(context.Background(), tc.agentID)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid || apiErr.Code != tc.code {
				t.Fatalf("Start(%q) error = %#v, want invalid %s", tc.agentID, err, tc.code)
			}
		})
	}
	if opener.calls != 0 {
		t.Fatalf("OpenCommandTerminal calls = %d, want 0", opener.calls)
	}
}

func TestStartFallsBackToAgentResolvedBinaryOutsidePATH(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	resolver := managedExecutableResolver{agentID: "claude-code", path: "/Users/test/.claude/local/claude"}
	svc := NewWithAgentResolver(resolver, resolver, opener, "")
	svc.selfExecutable = func() (string, error) { return "/Applications/Tallpine.app/Contents/MacOS/tallpine", nil }

	_, err := svc.Start(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Start(claude-code): %v", err)
	}
	if got := opener.input.Argv; !reflect.DeepEqual(got, []string{"/Applications/Tallpine.app/Contents/MacOS/tallpine", "claude-login", "--executable", "/Users/test/.claude/local/claude"}) {
		t.Fatalf("terminal argv = %#v, want trusted Claude login menu with adapter-resolved binary", got)
	}
}

func TestStartOpensCodexLoginMethodMenu(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	resolver := managedExecutableResolver{agentID: "codex", path: "/managed/bin/codex"}
	svc := NewWithAgentResolver(resolver, resolver, opener, "")
	svc.selfExecutable = func() (string, error) { return "/Applications/Tallpine.app/Contents/MacOS/tallpine", nil }

	_, err := svc.Start(context.Background(), "codex")
	if err != nil {
		t.Fatalf("Start(codex): %v", err)
	}
	want := []string{"/Applications/Tallpine.app/Contents/MacOS/tallpine", "codex-login", "--executable", "/managed/bin/codex", "--use-default-credential-store"}
	if got := opener.input.Argv; !reflect.DeepEqual(got, want) {
		t.Fatalf("terminal argv = %#v, want trusted Codex login menu", got)
	}
}

func TestStartPrefersAdapterResolvedBinaryOverGenericPATHMatch(t *testing.T) {
	t.Parallel()

	opener := &recordingTerminalOpener{}
	resolver := managedExecutableResolver{agentID: "kiro", path: "/validated/kiro/kiro-cli"}
	svc := NewWithAgentResolver(foundExecutable("kiro-cli"), resolver, opener, "")

	_, err := svc.Start(context.Background(), "kiro")
	if err != nil {
		t.Fatalf("Start(kiro): %v", err)
	}
	if got := opener.input.Argv; !reflect.DeepEqual(got, []string{"/validated/kiro/kiro-cli", "login"}) {
		t.Fatalf("terminal argv = %#v, want adapter-validated Kiro binary", got)
	}
}

type recordingTerminalOpener struct {
	calls    int
	input    shellterm.OpenCommandTerminalInput
	terminal shellterm.ShellTerminal
}

type managedExecutableResolver struct {
	agentID string
	path    string
}

func (m managedExecutableResolver) LookPath(string) (string, error) {
	return "", errors.New("not found on PATH")
}

func (m managedExecutableResolver) ResolveAgentBinary(_ context.Context, agentID string) (string, error) {
	if agentID != m.agentID {
		return "", errors.New("unknown agent")
	}
	return m.path, nil
}

func foundExecutable(executable string) ExecutableFinder {
	return executableFinderFunc(func(name string) (string, error) {
		if name != executable {
			return "", errors.New("not found")
		}
		return "/test/bin/" + executable, nil
	})
}

func (o *recordingTerminalOpener) OpenCommandTerminal(_ context.Context, in shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error) {
	o.calls++
	o.input = in
	return o.terminal, nil
}

func TestLogoutUsesAdapterExecutableAndNeverLoginLauncher(t *testing.T) {
	for _, tc := range []struct {
		agent string
		args  []string
	}{
		{"claude-code", []string{"auth", "logout"}},
		{"codex", []string{"logout"}},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			opener := &recordingTerminalOpener{terminal: shellterm.ShellTerminal{HandleID: "logout-terminal"}}
			resolver := managedExecutableResolver{agentID: tc.agent, path: "/managed/agent"}
			svc := NewWithAgentResolver(nil, resolver, opener, "")
			result, err := svc.Logout(context.Background(), tc.agent)
			if err != nil {
				t.Fatal(err)
			}
			want := append([]string{"/managed/agent"}, tc.args...)
			if !reflect.DeepEqual(opener.input.Argv, want) {
				t.Fatalf("argv = %v, want %v", opener.input.Argv, want)
			}
			if result.Action != "logout" || result.Terminal.HandleID != "logout-terminal" || result.TerminalInput != "" || opener.input.InitialInput != "" {
				t.Fatalf("unexpected logout: %+v, input %+v", result, opener.input)
			}
		})
	}
}

func TestLogoutRejectsUnknownUnsupportedAndMissingExecutable(t *testing.T) {
	for _, tc := range []struct{ agent, code string }{
		{"not-a-harness", "AGENT_AUTH_TARGET_UNKNOWN"},
		{"kiro", "AGENT_LOGOUT_UNSUPPORTED"},
		{"aider", "AGENT_AUTH_TARGET_UNKNOWN"},
		{"codex", "AGENT_AUTH_UNAVAILABLE"},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			opener := &recordingTerminalOpener{}
			_, err := New(foundExecutables(nil), opener).Logout(context.Background(), tc.agent)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if opener.calls != 0 {
				t.Fatal("rejected logout opened a terminal")
			}
		})
	}
}
