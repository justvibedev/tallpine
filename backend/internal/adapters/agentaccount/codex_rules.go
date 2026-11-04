package agentaccount

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// codexAllowedSubcommands are the CLI calls a worker needs from inside the
// sandbox: reporting, messaging, committing and reading session state.
var codexAllowedSubcommands = [][]string{
	{"report"}, {"send"}, {"commit"}, {"session", "get"}, {"session", "usage"}, {"session", "ls"},
}

// codexManagedRulesFile is the execpolicy file Tallpine owns inside a managed
// account home.
const codexManagedRulesFile = "tallpine.rules"

// Codex's workspace-write sandbox cannot reach the daemon socket, so every
// `tallpine` call from a Codex terminal session would ask for approval. This
// rule runs exactly that command outside the sandbox. It is written only into the
// account homes Tallpine creates under its own data directory, which a session's
// sandbox cannot write; the computer's own Codex home and a project's
// .codex/rules are never touched.
func codexManagedRules() []byte {
	var b strings.Builder
	b.WriteString("# Managed by Tallpine; rewritten before every launch. Edits are lost.\n")
	// Explicit subcommands only: an allowed unsandboxed `tallpine` must not be
	// able to start arbitrary programs (agent-process supervise) or write host
	// files (browser). Keep in step with the chat auto-approve allowlist in
	// codexappserver/tallpine_autoapprove.go.
	for _, name := range []string{brand.CLIName, brand.CLIAlias} {
		for _, sub := range codexAllowedSubcommands {
			parts := append([]string{name}, sub...)
			quoted := make([]string, len(parts))
			for i, p := range parts {
				quoted[i] = fmt.Sprintf("%q", p)
			}
			fmt.Fprintf(&b, "prefix_rule(pattern=[%s], decision=\"allow\")\n", strings.Join(quoted, ", "))
		}
	}
	return []byte(b.String())
}

// prepareCodexRules makes home/rules a real directory holding the managed rule
// plus links to the computer's own rule files, so the user's rules still apply on
// the account. A `rules` link left by an earlier release is removed first: it
// pointed into the computer's own home, which must never receive this file.
func (c *Codex) prepareCodexRules(home string) error {
	if sameDir(home, c.DeviceHome) {
		return nil
	}
	dir := filepath.Join(home, "rules")
	info, err := os.Lstat(dir)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("replace rules link: %w", err)
		}
	case err == nil && !info.IsDir():
		return fmt.Errorf("rules in %s is not a directory", home)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	deviceRules := filepath.Join(c.DeviceHome, "rules")
	keep := map[string]bool{codexManagedRulesFile: true}
	entries, _ := os.ReadDir(deviceRules)
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == codexManagedRulesFile {
			continue
		}
		keep[entry.Name()] = true
		link := filepath.Join(dir, entry.Name())
		if cur, err := os.Readlink(link); err == nil && cur == filepath.Join(deviceRules, entry.Name()) {
			continue
		}
		_ = os.Remove(link)
		if err := os.Symlink(filepath.Join(deviceRules, entry.Name()), link); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("link rule %s: %w", entry.Name(), err)
		}
	}
	// Drop links to rule files the computer no longer has.
	existing, _ := os.ReadDir(dir)
	for _, entry := range existing {
		if entry.Type()&os.ModeSymlink != 0 && !keep[entry.Name()] {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	managed := filepath.Join(dir, codexManagedRulesFile)
	if fi, err := os.Lstat(managed); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(managed) // never write through a link
	}
	return writeIfChanged(managed, codexManagedRules())
}

// sameDir reports whether a and b are the same directory once links resolve.
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
