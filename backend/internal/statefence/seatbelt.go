package statefence

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/securitymode"
	"tallpine.eu/tallpine/backend/internal/brand"
)

// SandboxExec is macOS's seatbelt launcher. A var so tests can point it at a
// missing path.
var SandboxExec = "/usr/bin/sandbox-exec"

// ErrNoRunFile means the launch env does not name the Tallpine run file, so the
// state to deny cannot be located. Callers fail the launch rather than start
// an agent behind an empty fence; the API reports it as POLICY_REQUIRES_FENCE.
var ErrNoRunFile error = agentpolicy.ErrFenceNoRunFile

// RequireRunFile returns ErrNoRunFile when env cannot locate the state a fence
// must deny. Every harness's fence depends on it, including Claude Code's, whose
// settings builders cannot return an error and so check it before launching.
func RequireRunFile(env map[string]string) error {
	if strings.TrimSpace(env[brand.Env("RUN_FILE")]) == "" {
		return ErrNoRunFile
	}
	return nil
}

// HarnessPaths are a harness's own state the fence must keep working: Write
// dirs it creates and updates (sessions, logs, its token store), and AuthRead
// files inside a hidden credential home that its sign-in needs, re-allowed only
// under the hardened profile. Entries are absolute or ~/-relative.
type HarnessPaths struct {
	Write    []string
	AuthRead []string
}

// Available reports whether a seatbelt fence can run on goos: macOS with
// sandbox-exec present.
func Available(goos string) bool {
	if goos != "darwin" {
		return false
	}
	st, err := os.Stat(SandboxExec)
	return err == nil && !st.IsDir()
}

// Applies reports whether a launch under decision on goos runs fenced.
func Applies(decision agentpolicy.Decision, goos string) bool {
	return protectstate.Of(decision).Enabled() && Available(goos)
}

// Wrap runs argv inside the seatbelt profile. The profile is passed inline, so
// nothing is stored that the agent could rewrite before a relaunch.
func Wrap(argv []string, profile string) []string {
	out := make([]string, 0, len(argv)+3)
	out = append(out, SandboxExec, "-p", profile)
	return append(out, argv...)
}

// Profile builds the seatbelt profile for a launch: everything is allowed
// (network, Mach, Keychain) except reading or writing Tallpine state, writing
// outside workspace and the session's writable set, and, under the hardened
// profile, reading credential homes. workspace is the agent's checkout (the
// common git dir of the project repo on record in decision is writable, the
// files the owner's git trusts are not; see gitFence); empty grants no checkout.
// Seatbelt applies the last matching rule, so the order of the sections below
// is the precedence.
func Profile(env map[string]string, decision agentpolicy.Decision, workspace string, extra HarnessPaths) (string, error) {
	if err := RequireRunFile(env); err != nil {
		return "", err
	}
	runFile := strings.TrimSpace(env[brand.Env("RUN_FILE")])
	out := protectstate.Of(decision)
	home := strings.TrimSpace(env["HOME"])
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	resolve := func(p string) string { return ResolveCredentialPath(home, p) }

	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")

	// Writes: deny all, then open the writable set.
	b.WriteString("(deny file-write*)\n")
	writes := WritePaths(env, out.DevTooling())
	// Terminals, /dev/null and /dev/fd.
	writes = append(writes, "/dev")
	git := gitWriteSet(decision.GitRepo(), workspace)
	if workspace != "" {
		writes = append(writes, workspace)
	}
	if securitymode.Of(decision).AllowDocker() {
		writes = append(writes, DockerSocketPaths(env)...)
	}
	for _, p := range extra.Write {
		writes = append(writes, resolve(p))
	}
	allowWrites(&b, writes)
	git.writeRules(&b)
	denyCheckoutEntries(&b, workspace)

	// Credential homes (hardened only), then the harness's own auth files.
	if creds := CredentialsFor(decision, env); creds != nil {
		rule(&b, "deny file-read*", creds.Deny)
		reAllow := append([]string(nil), creds.ReAllow...)
		for _, p := range extra.AuthRead {
			reAllow = append(reAllow, resolve(p))
		}
		allowReads(&b, reAllow)
	}

	// Tallpine state last, so no opening above can reach it.
	state := StatePaths(runFile, DataDir(env))
	rule(&b, "deny file-read* file-write*", append(state.Files, state.Dirs...))
	denySockets(&b, TmuxSocketPaths())
	return b.String(), nil
}

// denySockets refuses connecting to each unix socket. File rules do not govern
// connect(2), and a symlink to the socket still matches its resolved path.
func denySockets(b *strings.Builder, paths []string) {
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		for _, v := range pathForms(filepath.Clean(p)) {
			if !seen[v] {
				seen[v] = true
				b.WriteString("(deny network-outbound (remote unix-socket (path-literal " + quote(v) + ")))\n")
			}
		}
	}
}

// rule writes one seatbelt rule over paths as subpaths (a file matches its own
// subpath), each listed raw and resolved, since seatbelt matches resolved
// paths. Entries with * become anchored regexes over a single path segment.
func rule(b *strings.Builder, action string, paths []string) {
	emitRule(b, action, paths, pathForms, filter)
}

// allowWrites opens paths for writing. Unlike rule it never follows a path's
// last component: a write grant covers that directory entry itself, so a fenced
// process can replace it with a symlink, and resolving it at the next launch
// would grant the link's target.
func allowWrites(b *strings.Builder, paths []string) {
	emitRule(b, "allow file-write*", paths, grantForms, filter)
}

// allowReads re-allows reading paths after the credential deny. As in
// allowWrites it never follows a path's last component: harness auth homes are
// write grants too, so a link planted there would otherwise re-open its target.
func allowReads(b *strings.Builder, paths []string) {
	emitRule(b, "allow file-read*", paths, grantForms, filter)
}

// allowBelow opens everything under dir for writing but not dir itself, so a
// fenced process cannot rename dir away or swap it, which would also get past
// any deny on a path inside it.
func allowBelow(b *strings.Builder, dir string) {
	emitRule(b, "allow file-write*", []string{dir}, grantForms, func(p string) string {
		return "(regex " + quote("^"+regexp.QuoteMeta(p)+"/") + ")"
	})
}

func emitRule(b *strings.Builder, action string, paths []string, forms func(string) []string, toFilter func(string) string) {
	var filters []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		for _, v := range forms(filepath.Clean(p)) {
			f := toFilter(v)
			if !seen[f] {
				seen[f] = true
				filters = append(filters, f)
			}
		}
	}
	if len(filters) == 0 {
		return
	}
	b.WriteString("(" + action + "\n")
	for _, f := range filters {
		b.WriteString("  " + f + "\n")
	}
	b.WriteString(")\n")
}

func filter(p string) string {
	if !strings.Contains(p, "*") {
		return "(subpath " + quote(p) + ")"
	}
	parts := strings.Split(p, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	return "(regex " + quote("^"+strings.Join(parts, "[^/]*")+"$") + ")"
}

// quote is an SBPL string literal: backslash and double quote escaped.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// grantForms is p and p with only its parent resolved: the /tmp -> /private/tmp
// form seatbelt matches, without trusting a last component that may be a link.
func grantForms(p string) []string {
	dirs := pathForms(filepath.Dir(p))
	forms := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		forms = append(forms, filepath.Join(dir, filepath.Base(p)))
	}
	return forms
}

// pathForms is p and its symlink-resolved form. A path that does not exist yet
// resolves through its longest existing parent (/tmp/x -> /private/tmp/x).
func pathForms(p string) []string {
	forms := []string{p}
	dir, rest := p, ""
	for {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			if resolved := filepath.Join(r, rest); resolved != p {
				forms = append(forms, resolved)
			}
			return forms
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return forms
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
