package ghaccount

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const statusFixture = `github.com
  ✓ Logged in to github.com account octo (keyring)
  - Active account: true
  - Git operations protocol: ssh

  ✓ Logged in to github.com account hub-two (keyring)
  - Active account: false

  X Failed to log in to github.com account stale (keyring)
`

func fakeBin(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestExecGHAgainstFakeBinary(t *testing.T) {
	fakeBin(t, "gh", `case "$2" in
status) cat <<'EOF'
`+statusFixture+`EOF
exit 1;;
token) [ "$5" = "-u" ] && echo "secret-$6";;
esac`)
	logins, err := ExecGH{}.Logins(context.Background())
	if err != nil || !reflect.DeepEqual(logins, []string{"octo", "hub-two", "stale"}) {
		t.Fatalf("logins = %v, %v", logins, err)
	}
	tok, err := ExecGH{}.Token(context.Background(), "hub-two")
	if err != nil || tok != "secret-hub-two" {
		t.Fatalf("token = %q, %v", tok, err)
	}
}

func TestExecSSHHostParsesHostName(t *testing.T) {
	fakeBin(t, "ssh", `printf 'user git\nhostname github.com\nport 22\n'`)
	h, err := ExecSSHHost(context.Background(), "github-work")
	if err != nil || h != "github.com" {
		t.Fatalf("host = %q, %v", h, err)
	}
}

func TestExecSSHHostEndsOptionsBeforeAlias(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	fakeBin(t, "ssh", `printf '%s\n' "$@" > `+argsFile+`; echo hostname github.com`)
	if _, err := ExecSSHHost(context.Background(), "github-work"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(argsFile)
	if string(got) != "-G\n--\ngithub-work\n" {
		t.Fatalf("ssh args = %q, want -G -- <alias>", got)
	}
}

func TestParseRemoteRejectsOptionLikeHost(t *testing.T) {
	called := false
	ssh := func(context.Context, string) (string, error) { called = true; return githubHost, nil }
	if _, ok := ParseRemote(context.Background(), "-oProxyCommand=x:o/r", ssh); ok || called {
		t.Fatalf("option-like host accepted (ok=%v, sshCalled=%v)", ok, called)
	}
}

// A GH_TOKEN in the daemon env makes real gh ignore -u; the scrub must keep it
// out of the child so the account's own token is returned.
func TestExecGHIgnoresDaemonTokenEnv(t *testing.T) {
	fakeBin(t, "gh", `if [ -n "$GH_TOKEN$GITHUB_TOKEN" ]; then echo "  ✓ Logged in to github.com account daemon-pinned"; echo env-token; exit 0; fi
case "$2" in
token) echo "keyring-$6";;
status) echo "  ✓ Logged in to github.com account keyring-user";;
esac`)
	t.Setenv("GH_TOKEN", "daemon-pinned")
	t.Setenv("GITHUB_TOKEN", "daemon-pinned")
	tok, err := ExecGH{}.Token(context.Background(), "acme")
	if err != nil || tok != "keyring-acme" {
		t.Fatalf("token = %q, %v; want keyring-acme", tok, err)
	}
	logins, _ := ExecGH{}.Logins(context.Background())
	if len(logins) != 1 || logins[0] != "keyring-user" {
		t.Fatalf("logins = %v", logins)
	}
}

func TestScrubbedEnv(t *testing.T) {
	got := ScrubbedEnv([]string{"PATH=/bin", "gh_token=x", "GITHUB_TOKEN=y", "HOME=/h"})
	if !reflect.DeepEqual(got, []string{"PATH=/bin", "HOME=/h"}) {
		t.Fatalf("got %v", got)
	}
}
