package statefence

import (
	"errors"
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
// an agent behind an empty fence.
var ErrNoRunFile = errors.New("statefence: launch env has no run file; cannot fence Tallpine state")

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
// profile, reading credential homes. workspace is the agent's checkout (its
// linked git dir is writable, the shared config and hooks are not); empty
// grants no checkout. Seatbelt applies the last matching rule, so the order of
// the sections below is the precedence.
func Profile(env map[string]string, decision agentpolicy.Decision, workspace string, extra HarnessPaths) (string, error) {
	runFile := strings.TrimSpace(env[brand.Env("RUN_FILE")])
	if runFile == "" {
		return "", ErrNoRunFile
	}
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
	gitDir, commonDir := gitDirs(workspace)
	if workspace != "" {
		writes = append(writes, workspace)
	}
	if gitDir != "" {
		writes = append(writes, gitDir)
	}
	if commonDir != "" {
		writes = append(writes, commonDir)
	}
	if securitymode.Of(decision).AllowDocker() {
		writes = append(writes, DockerSocketPaths(env)...)
	}
	for _, p := range extra.Write {
		writes = append(writes, resolve(p))
	}
	rule(&b, "allow file-write*", writes)
	if commonDir != "" {
		// The shared config and hooks run unfenced in the owner's git.
		rule(&b, "deny file-write*", []string{filepath.Join(commonDir, "config"), filepath.Join(commonDir, "hooks")})
	}

	// Credential homes (hardened only), then the harness's own auth files.
	if creds := CredentialsFor(decision, env); creds != nil {
		rule(&b, "deny file-read*", creds.Deny)
		reAllow := append([]string(nil), creds.ReAllow...)
		for _, p := range extra.AuthRead {
			reAllow = append(reAllow, resolve(p))
		}
		rule(&b, "allow file-read*", reAllow)
	}

	// Tallpine state last, so no opening above can reach it.
	state := StatePaths(runFile, DataDir(env))
	rule(&b, "deny file-read* file-write*", append(state.Files, state.Dirs...))
	return b.String(), nil
}

// rule writes one seatbelt rule over paths as subpaths (a file matches its own
// subpath), each listed raw and resolved, since seatbelt matches resolved
// paths. Entries with * become anchored regexes over a single path segment.
func rule(b *strings.Builder, action string, paths []string) {
	var filters []string
	seen := map[string]bool{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		for _, v := range pathForms(filepath.Clean(p)) {
			f := filter(v)
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

// gitDirs is workspace's git dir and the common dir it shares objects and refs
// with. A linked worktree's .git file names its private git dir, whose
// commondir file names the shared one; a plain checkout has one .git dir.
func gitDirs(workspace string) (gitDir, commonDir string) {
	if workspace == "" {
		return "", ""
	}
	dotGit := filepath.Join(workspace, ".git")
	st, err := os.Stat(dotGit)
	if err != nil {
		return "", ""
	}
	if st.IsDir() {
		return "", dotGit
	}
	data, err := os.ReadFile(dotGit)
	if err != nil {
		return "", ""
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return "", ""
	}
	gitDir = strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(workspace, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	commonDir = gitDir
	if c, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(c))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
		commonDir = filepath.Clean(commonDir)
	}
	return gitDir, commonDir
}
