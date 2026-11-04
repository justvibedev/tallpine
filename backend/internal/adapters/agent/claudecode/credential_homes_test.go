package claudecode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

const hardenedDoc = `{"schemaVersion":2,"profile":"hardened"}`

func credEnv(home string) map[string]string {
	return map[string]string{
		brand.Env("RUN_FILE"): filepath.Join(home, "state", "running.json"),
		"HOME":                home,
		"SSH_AUTH_SOCK":       "/private/tmp/agent.sock",
	}
}

func sandboxOf(t *testing.T, s map[string]any) (deny, allow, socks []string) {
	t.Helper()
	sb := s["sandbox"].(map[string]any)
	fs := sb["filesystem"].(map[string]any)
	deny, _ = fs["denyRead"].([]string)
	allow, _ = fs["allowRead"].([]string)
	socks = sb["network"].(map[string]any)["allowUnixSockets"].([]string)
	return
}

func hasPath(list []string, p string) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

func TestHardenedDeniesCredentialHomes(t *testing.T) {
	home := t.TempDir()
	deny, allow, socks := sandboxOf(t, ProtectStateSettingsFor(credEnv(home), policytest.Hardened(t)))
	for _, rel := range []string{".ssh", ".aws", ".kube", ".config/gcloud", ".azure", ".docker/config.json", ".netrc", ".npmrc", ".pypirc", ".config/gh", ".git-credentials"} {
		if !hasPath(deny, filepath.Join(home, rel)) {
			t.Errorf("denyRead misses ~/%s", rel)
		}
	}
	if !hasPath(allow, filepath.Join(home, ".ssh/known_hosts")) {
		t.Error("known_hosts must stay readable so ssh verifies hosts")
	}
	if !hasPath(socks, "/private/tmp/agent.sock") {
		t.Error("the ssh agent socket must stay reachable")
	}
}

// A user-layer allow cannot undo the org's hardened fence.
func TestUserCredentialAllowIgnoredUnderHardened(t *testing.T) {
	home := t.TempDir()
	eng2, _ := policytest.Managed(t, hardenedDoc, map[string]map[string]any{"protectState": {"credentialAllow": []string{"~/.ssh", "~/.aws"}}})
	deny, _, _ := sandboxOf(t, ProtectStateSettingsFor(credEnv(home), eng2.DecideStatic(context.Background())))
	if !hasPath(deny, filepath.Join(home, ".ssh")) || !hasPath(deny, filepath.Join(home, ".aws")) {
		t.Fatalf("user-layer credentialAllow loosened the hardened fence: %v", deny)
	}
}

func TestRestoreEnvCarriesSSHAgentSocket(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/private/tmp/restore.sock")
	home := t.TempDir()
	env := statefence.RestoreEnv(filepath.Join(home, "state", "running.json"), "", ports.SessionRef{})
	env["HOME"] = home
	_, _, socks := sandboxOf(t, ProtectStateSettingsFor(env, policytest.Hardened(t)))
	if !hasPath(socks, "/private/tmp/restore.sock") {
		t.Fatalf("restored hardened session lost the ssh agent socket: %v", socks)
	}
}

func TestNonHardenedKeepsCredentialHomes(t *testing.T) {
	home := t.TempDir()
	on := policytest.Decision(t, map[string]map[string]any{"protectState": {"mode": "on"}})
	deny, allow, _ := sandboxOf(t, ProtectStateSettingsFor(credEnv(home), on))
	if hasPath(deny, filepath.Join(home, ".ssh")) || len(allow) != 0 {
		t.Fatalf("credential homes hidden without the hardened profile: %v", deny)
	}
}

func TestCredentialPathsAndAllowList(t *testing.T) {
	home := t.TempDir()
	engine, _ := policytest.Managed(t, `{"schemaVersion":2,"profile":"hardened","rules":{"protectState":{"credentialAllow":["~/.npmrc","~/.ssh"]}}}`,
		map[string]map[string]any{"protectState": {"credentialPaths": []string{"~/.config/op", "/opt/secrets"}}})
	d := engine.DecideStatic(context.Background())
	deny, allow, _ := sandboxOf(t, ProtectStateSettingsFor(credEnv(home), d))
	if !hasPath(deny, filepath.Join(home, ".config/op")) || !hasPath(deny, "/opt/secrets") {
		t.Errorf("extra paths not denied: %v", deny)
	}
	if hasPath(deny, filepath.Join(home, ".npmrc")) || hasPath(deny, filepath.Join(home, ".ssh")) {
		t.Errorf("allow-listed paths still denied: %v", deny)
	}
	if len(allow) != 0 {
		t.Errorf("no ssh re-allow needed when ~/.ssh is allowed: %v", allow)
	}
	if !hasPath(deny, filepath.Join(home, ".aws")) {
		t.Error("allow-list must not drop other defaults")
	}
}

// TestFencedChildCannotReadCredentials runs a child under a Seatbelt profile
// built from the fence's denyRead list and checks it reads nothing denied, and
// still reads known_hosts and an unrelated file.
func TestFencedChildCannotReadCredentials(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Seatbelt probe is macOS only")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	files := map[string]string{".ssh/id_ed25519": "k", ".ssh/known_hosts": "h", ".aws/credentials": "a", ".netrc": "n", ".config/gh/hosts.yml": "g", "notes.txt": "ok"}
	for rel, body := range files {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	deny, allow, _ := sandboxOf(t, ProtectStateSettingsFor(credEnv(home), policytest.Hardened(t)))
	var prof strings.Builder
	prof.WriteString("(version 1)(allow default)")
	for _, p := range deny {
		prof.WriteString(`(deny file-read* (subpath "` + p + `"))`)
	}
	for _, p := range allow {
		prof.WriteString(`(allow file-read* (literal "` + p + `"))`)
	}
	read := func(rel string) bool {
		out, err := exec.Command("/usr/bin/sandbox-exec", "-p", prof.String(), "/bin/cat", filepath.Join(home, rel)).CombinedOutput()
		if err != nil && strings.Contains(string(out), "sandbox_apply") {
			t.Skipf("nested sandbox unavailable: %s", out)
		}
		return err == nil
	}
	for _, rel := range []string{".ssh/id_ed25519", ".aws/credentials", ".netrc", ".config/gh/hosts.yml"} {
		if read(rel) {
			t.Errorf("fenced child read ~/%s", rel)
		}
	}
	for _, rel := range []string{".ssh/known_hosts", "notes.txt"} {
		if !read(rel) {
			t.Errorf("fenced child cannot read ~/%s", rel)
		}
	}
}
