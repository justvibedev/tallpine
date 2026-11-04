// Package skillbridge finds the skills and instruction files a project offers
// under every agent convention (.claude, .agents, .codex) and works out what a
// given harness cannot see. Claude Code reads CLAUDE.md and .claude/skills;
// Codex reads AGENTS.md and .agents/skills. Discover and Statuses only read;
// Offers and ApplyOffer let the user opt in to creating the missing files in
// the project checkout.
package skillbridge

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Harness identifiers the bridge reasons about. Other harnesses are reported as
// unverified and never bridged.
const (
	HarnessClaude = "claude-code"
	HarnessCodex  = "codex"
)

// Scope says who owns a skill source.
type Scope string

const (
	// ScopeRepo is a skill inside the project worktree.
	ScopeRepo Scope = "repo"
	// ScopeUser is a skill in the account's home directories.
	ScopeUser Scope = "user"
	// ScopeTallpine is a skill Tallpine installs under its data dir.
	ScopeTallpine Scope = "tallpine"
)

// Convention is the directory family a skill lives in.
type Convention string

const (
	// ConvClaude is a .claude/skills directory.
	ConvClaude Convention = "claude"
	// ConvAgents is a .agents/skills directory.
	ConvAgents Convention = "agents"
	// ConvCodex is a .codex/skills directory.
	ConvCodex Convention = "codex"
	// ConvTallpine is the data-dir skills directory.
	ConvTallpine Convention = "tallpine"
)

// Options locate the inputs. Home is the account home; ClaudeConfigDir is the
// project's CLAUDE_CONFIG_DIR when it sets one (Claude then reads skills there
// instead of ~/.claude). Empty fields skip their sources.
type Options struct {
	Workspace       string
	Home            string
	ClaudeConfigDir string
	DataDir         string
	// Committed reports whether a workspace-relative path is in the project's
	// HEAD commit. Sessions run in worktrees made from a commit, so an untracked
	// file in the checkout is invisible to them. Nil treats everything as
	// committed (no git, or nothing to check).
	Committed func(rel string) bool
}

// Skill is one discovered skill. Path is the skill directory.
type Skill struct {
	Name        string
	Description string
	Scope       Scope
	Convention  Convention
	Path        string
	// Pending marks a repo skill that is in the checkout but not committed, so
	// sessions (worktrees of a commit) do not get it yet.
	Pending bool
}

// Collision records a name clash: Winner is used, Loser is shadowed or skipped.
type Collision struct {
	Name   string
	Winner string
	Loser  string
	Reason string
}

// Inventory is the result of Discover. Skills holds every skill from every
// source in precedence order (duplicates by name included); Winners holds one
// per name.
type Inventory struct {
	Skills       []Skill
	Winners      []Skill
	Collisions   []Collision
	Instructions Instructions
	// PendingCommit lists workspace-relative instruction files and skill
	// directories that exist but are not committed.
	PendingCommit []string
}

// Instructions describes the project instruction files at the workspace root.
type Instructions struct {
	AgentsMD bool
	ClaudeMD bool // CLAUDE.md, .claude/CLAUDE.md or CLAUDE.local.md
	// RootClaudeMD is the ./CLAUDE.md case, the only one Codex can be pointed at.
	RootClaudeMD bool
	// ClaudeMDPending and AgentsMDPending: the file exists only uncommitted.
	ClaudeMDPending bool
	AgentsMDPending bool
}

type source struct {
	root  string
	scope Scope
	conv  Convention
}

func sources(o Options) []source {
	var out []source
	add := func(root string, s Scope, c Convention) {
		if root != "" {
			out = append(out, source{root, s, c})
		}
	}
	if o.Workspace != "" {
		add(filepath.Join(o.Workspace, ".claude", "skills"), ScopeRepo, ConvClaude)
		add(filepath.Join(o.Workspace, ".agents", "skills"), ScopeRepo, ConvAgents)
		add(filepath.Join(o.Workspace, ".codex", "skills"), ScopeRepo, ConvCodex)
	}
	claude := o.ClaudeConfigDir
	if claude == "" && o.Home != "" {
		claude = filepath.Join(o.Home, ".claude")
	}
	if claude != "" {
		add(filepath.Join(claude, "skills"), ScopeUser, ConvClaude)
	}
	if o.Home != "" {
		add(filepath.Join(o.Home, ".agents", "skills"), ScopeUser, ConvAgents)
		add(filepath.Join(o.Home, ".codex", "skills"), ScopeUser, ConvCodex)
	}
	if o.DataDir != "" {
		add(filepath.Join(o.DataDir, "skills"), ScopeTallpine, ConvTallpine)
	}
	return out
}

// Native reports whether a harness loads skills from a source without help.
// Claude: .claude (repo and user). Codex: .agents (repo and user) and the user
// ~/.codex/skills. Everything else needs a bridge.
func Native(harness string, s Scope, c Convention) bool {
	switch harness {
	case HarnessClaude:
		return c == ConvClaude
	case HarnessCodex:
		return c == ConvAgents || (c == ConvCodex && s == ScopeUser)
	}
	return false
}

// Discover inventories skills and instruction files. It only reads.
func Discover(o Options) Inventory {
	var inv Inventory
	seen := map[string]Skill{}
	for _, src := range sources(o) {
		entries, err := os.ReadDir(src.root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			dir := filepath.Join(src.root, name)
			if st, err := os.Stat(dir); err != nil || !st.IsDir() { // follows symlinks
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
				continue
			}
			_, desc := frontmatter(filepath.Join(dir, "SKILL.md"))
			sk := Skill{Name: name, Description: desc, Scope: src.scope, Convention: src.conv, Path: dir}
			if src.scope == ScopeRepo && o.Committed != nil {
				if rel, err := filepath.Rel(o.Workspace, dir); err == nil && !o.Committed(filepath.ToSlash(rel)) {
					sk.Pending = true
					inv.PendingCommit = append(inv.PendingCommit, filepath.ToSlash(rel))
				}
			}
			inv.Skills = append(inv.Skills, sk)
			if w, ok := seen[name]; ok {
				if !sameTarget(w.Path, dir) {
					inv.Collisions = append(inv.Collisions, Collision{Name: name, Winner: w.Path, Loser: dir, Reason: "name already provided by a higher-priority source"})
				}
				continue
			}
			seen[name] = sk
			inv.Winners = append(inv.Winners, sk)
		}
	}
	if o.Workspace != "" {
		inv.Instructions = Instructions{
			RootClaudeMD: isFile(filepath.Join(o.Workspace, "CLAUDE.md")),
			AgentsMD:     isFile(filepath.Join(o.Workspace, "AGENTS.md")),
			ClaudeMD: isFile(filepath.Join(o.Workspace, "CLAUDE.md")) ||
				isFile(filepath.Join(o.Workspace, ".claude", "CLAUDE.md")) ||
				isFile(filepath.Join(o.Workspace, "CLAUDE.local.md")),
		}
	}
	inv.markPendingInstructions(o)
	return inv
}

// markPendingInstructions flags instruction files that exist only uncommitted.
// CLAUDE.local.md is personal by design and never counts.
func (inv *Inventory) markPendingInstructions(o Options) {
	if o.Workspace == "" || o.Committed == nil {
		return
	}
	pending := func(rels ...string) (exists, allPending bool) {
		allPending = true
		for _, r := range rels {
			if !isFile(filepath.Join(o.Workspace, filepath.FromSlash(r))) {
				continue
			}
			exists = true
			if o.Committed(r) {
				allPending = false
			} else {
				inv.PendingCommit = append(inv.PendingCommit, r)
			}
		}
		return exists, exists && allPending
	}
	_, inv.Instructions.AgentsMDPending = pending("AGENTS.md")
	if exists, all := pending("CLAUDE.md", ".claude/CLAUDE.md"); exists {
		inv.Instructions.ClaudeMDPending = all
	}
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func sameTarget(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// frontmatter returns name and description from a SKILL.md YAML header.
func frontmatter(path string) (name, desc string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	if !strings.HasPrefix(s, "---\n") {
		return "", ""
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return "", ""
	}
	return strings.TrimSpace(fm.Name), strings.Join(strings.Fields(fm.Description), " ")
}
