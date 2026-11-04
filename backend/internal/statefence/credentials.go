package statefence

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
)

// DefaultCredentialHomes are the developer credential locations, relative to
// the user's home, that the hardened profile hides from agents. Workers keep
// pushing through Tallpine's own credential path (the githubCreds helper) and
// an SSH agent socket, neither of which reads these files.
var DefaultCredentialHomes = []string{
	".ssh", ".aws", ".kube", ".config/gcloud", ".azure", ".docker/config.json",
	".netrc", ".npmrc", ".pypirc",
	// gh and git credential stores.
	".config/gh", ".git-credentials", ".config/git/credentials",
}

// SSHReadable are files inside ~/.ssh that hold no secret and that ssh needs
// (host key verification and host aliases); they are re-allowed after the
// directory is denied so git over ssh with an agent keeps working.
var SSHReadable = []string{".ssh/known_hosts", ".ssh/known_hosts2", ".ssh/config"}

// Credentials is the credential part of a launch's fence.
type Credentials struct {
	Deny    []string
	ReAllow []string
	Sockets []string
}

// CredentialsFor resolves the fence for decision, or nil when the decision
// does not hide credentials (anything but the hardened profile).
func CredentialsFor(decision agentpolicy.Decision, env map[string]string) *Credentials {
	out := protectstate.Of(decision)
	if !out.HidesCredentials() {
		return nil
	}
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return nil
	}
	allow := map[string]bool{}
	// Loosening an org floor is the administrator's call: honour the allow-list
	// only from the managed file or the admin-set environment.
	if src := decision.Source(protectstate.Name, protectstate.FieldCredentialAllow); src == agentpolicy.SourceManaged || src == agentpolicy.SourceEnv {
		for _, a := range out.CredentialAllow() {
			allow[ResolveCredentialPath(home, a)] = true
		}
	} else if len(out.CredentialAllow()) > 0 {
		slog.Warn("protectState.credentialAllow ignored under the hardened profile: only the managed file may set it")
	}
	f := &Credentials{}
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] || allow[p] {
			return
		}
		seen[p] = true
		f.Deny = append(f.Deny, p)
	}
	for _, rel := range DefaultCredentialHomes {
		add(filepath.Join(home, rel))
	}
	for _, p := range out.CredentialPaths() {
		add(ResolveCredentialPath(home, p))
	}
	if !allow[filepath.Join(home, ".ssh")] && seen[filepath.Join(home, ".ssh")] {
		for _, rel := range SSHReadable {
			f.ReAllow = append(f.ReAllow, filepath.Join(home, rel))
		}
	}
	// The agent socket is how a worker signs with a key it cannot read.
	if sock := strings.TrimSpace(env["SSH_AUTH_SOCK"]); sock != "" {
		f.Sockets = append(f.Sockets, sock)
	}
	return f
}

// ResolveCredentialPath expands a configured entry: ~/ and relative entries are
// under home, absolute ones are kept.
func ResolveCredentialPath(home, p string) string {
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		return ""
	case p == "~":
		return home
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(home, p[2:])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	}
	return filepath.Join(home, p)
}
