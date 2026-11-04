package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

type credServer struct {
	status int
	body   string
	got    []apitypes.GitHubCredentialRequest
	paths  []string
}

func (s *credServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apitypes.GitHubCredentialRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.got = append(s.got, req)
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_, _ = io.WriteString(w, s.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

const credOK = `{"username":"x-access-token","token":"tok-123","source":"account"}`

func credCLI(t *testing.T, s *credServer, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, s.start(t))
	t.Setenv("TALLPINE_SESSION_ID", "p-1")
	return executeCLI(t, Deps{In: strings.NewReader(stdin), ProcessAlive: func(int) bool { return true }}, args...)
}

func TestGitCredentialGetPrintsTheTokenForGitToUseOnce(t *testing.T) {
	s := &credServer{status: 200, body: credOK}
	out, stderr, err := credCLI(t, s, "protocol=https\nhost=github.com\npath=acme/app.git\n\nignored=after-blank\n", "git-credential", "get")
	if err != nil || out != "username=x-access-token\npassword=tok-123\n\n" {
		t.Fatalf("out=%q err=%v stderr=%q", out, err, stderr)
	}
	if len(s.got) != 1 || s.got[0].Host != "github.com" || s.got[0].Protocol != "https" || s.paths[0] != "POST /api/v1/sessions/p-1/github-credential" {
		t.Fatalf("request = %+v %v", s.got, s.paths)
	}
	if strings.Contains(stderr, "tok-123") {
		t.Fatal("the token must never reach stderr")
	}
}

func TestGitCredentialRefusalsGiveGitNothingAndSayWhy(t *testing.T) {
	for code, want := range map[string]string{
		"GITHUB_CREDENTIALS_DENIED":      "disabled by the agent policy",
		"GITHUB_CREDENTIAL_UNAVAILABLE":  "no GitHub token is available",
		"GITHUB_HOST_NOT_ALLOWED":        "not one of this project's GitHub hosts",
		"GITHUB_CREDENTIALS_NOT_ENABLED": "holds its own GitHub token",
	} {
		s := &credServer{status: 403, body: `{"error":"forbidden","code":"` + code + `","message":"m"}`}
		out, stderr, err := credCLI(t, s, "protocol=https\nhost=github.com\n\n", "git-credential", "get")
		if err != nil || out != "" || !strings.Contains(stderr, want) {
			t.Errorf("%s: out=%q err=%v stderr=%q", code, out, err, stderr)
		}
	}
}

func TestGitCredentialStoreEraseAndHostlessGetDoNothing(t *testing.T) {
	s := &credServer{status: 200, body: credOK}
	for _, args := range [][]string{{"git-credential", "store"}, {"git-credential", "erase"}} {
		if out, _, err := credCLI(t, s, "protocol=https\nhost=github.com\nusername=u\npassword=p\n\n", args...); err != nil || out != "" {
			t.Fatalf("%v: %q %v", args, out, err)
		}
	}
	if out, _, err := credCLI(t, s, "protocol=https\n\n", "git-credential", "get"); err != nil || out != "" {
		t.Fatalf("a request with no host: %q %v", out, err)
	}
	if len(s.got) != 0 {
		t.Fatalf("store/erase and hostless get must not call the daemon: %+v", s.got)
	}
}

func TestGitCredentialNeedsASession(t *testing.T) {
	cfg := setConfigEnv(t)
	writeRunFileFor(t, cfg, (&credServer{status: 200, body: credOK}).start(t))
	t.Setenv("TALLPINE_SESSION_ID", "")
	_, _, err := executeCLI(t, Deps{In: strings.NewReader("host=github.com\n\n"), ProcessAlive: func(int) bool { return true }}, "git-credential", "get")
	if ExitCode(err) != 2 {
		t.Fatalf("outside a session: %v (exit %d)", err, ExitCode(err))
	}
}

// fakeGH installs a `gh` that prints what it was given, in dir.
func fakeGH(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestGHExecGivesTheTokenToTheChildOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh fake")
	}
	root := t.TempDir()
	realDir, shimDir := filepath.Join(root, "real"), filepath.Join(root, "shim")
	fakeGH(t, realDir, `echo "token=$GH_TOKEN args=$*"`)
	fakeGH(t, shimDir, `echo "SHIM RAN"; exit 9`) // must never be picked
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+realDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(agentlaunch.EnvGHShimDir, shimDir)
	t.Setenv("GH_TOKEN", "")
	s := &credServer{status: 200, body: credOK}
	out, _, err := credCLI(t, s, "", "gh-exec", "--", "pr", "view", "--json", "title")
	if err != nil || strings.TrimSpace(out) != "token=tok-123 args=pr view --json title" {
		t.Fatalf("out=%q err=%v", out, err)
	}
	if os.Getenv("GH_TOKEN") != "" {
		t.Fatal("the token must live in the child's environment only, never this process's")
	}
	if len(s.got) != 1 || s.got[0].Host != "github.com" {
		t.Fatalf("%+v", s.got)
	}
}

func TestGHExecRunsWithoutATokenWhenRefusedAndPropagatesExitCodes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh fake")
	}
	realDir := filepath.Join(t.TempDir(), "real")
	fakeGH(t, realDir, `echo "token=[$GH_TOKEN]"; exit 3`)
	t.Setenv("PATH", realDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "")
	s := &credServer{status: 403, body: `{"error":"forbidden","code":"GITHUB_CREDENTIALS_DENIED","message":"m"}`}
	out, stderr, err := credCLI(t, s, "", "gh-exec", "--", "auth", "status")
	if strings.TrimSpace(out) != "token=[]" || !strings.Contains(stderr, "disabled by the agent policy") {
		t.Fatalf("a refusal runs gh with no token and says why: out=%q stderr=%q", out, stderr)
	}
	if ExitCode(err) != 3 {
		t.Fatalf("gh's exit status must be this command's: %v (exit %d)", err, ExitCode(err))
	}
}

func TestFindRealGHSkipsTheShimDirAndReportsAMissingGH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh fake")
	}
	root := t.TempDir()
	shim, realDir := filepath.Join(root, "shim"), filepath.Join(root, "real")
	fakeGH(t, shim, "true")
	if _, err := findRealGH(shim, shim); err == nil {
		t.Fatal("only the shim on PATH: gh is not installed")
	}
	fakeGH(t, realDir, "true")
	got, err := findRealGH(shim+string(os.PathListSeparator)+realDir, shim)
	if err != nil || got != filepath.Join(realDir, "gh") {
		t.Fatalf("%q %v", got, err)
	}
}
