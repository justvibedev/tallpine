package contextbudget

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// skillRoots are the repository directories whose SKILL.md name and
// description every session of a harness loads on start.
var skillRoots = []string{".claude/skills", ".agents/skills"}

// MeasureSkills sizes the always-loaded part of each repository skill: its
// name and description. The body loads only when the skill is used.
func MeasureSkills(ctx context.Context, src Source) ([]File, error) {
	if src.List == nil {
		return nil, nil
	}
	var out []File
	for _, root := range skillRoots {
		paths, err := src.List(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			if filepath.Base(p) != "SKILL.md" {
				continue
			}
			data, ok, err := src.Read(ctx, p)
			if err != nil || !ok {
				continue // an unreadable skill is skipped, not fatal
			}
			n := len(skillHead(string(data)))
			out = append(out, File{Path: p, Bytes: n, Tokens: Tokens(n)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tokens > out[j].Tokens })
	return out, nil
}

var skillKeyRe = regexp.MustCompile(`^(name|description):`)

// skillHead returns the frontmatter lines that name and describe a skill.
func skillHead(md string) string {
	lines := strings.Split(md, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return ""
	}
	var b strings.Builder
	keep := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		switch {
		case skillKeyRe.MatchString(line):
			keep = true
		case line != "" && line[0] != ' ' && line[0] != '\t':
			keep = false // another top-level key
		}
		if keep {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Memory is the Claude orchestrator memory of a project: MEMORY.md is loaded
// on every start, the other files on demand.
type Memory struct {
	Dir         string
	IndexBytes  int
	IndexTokens int
	Files       int
	DirTokens   int
}

// MeasureMemory sizes a Claude memory directory; ok is false when it has no
// MEMORY.md. The archive subdirectory is not counted: archived entries are no
// longer part of what Claude reads.
func MeasureMemory(dir string) (Memory, bool, error) {
	index, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if os.IsNotExist(err) {
		return Memory{}, false, nil
	}
	if err != nil {
		return Memory{}, false, err
	}
	m := Memory{Dir: dir, IndexBytes: len(index), IndexTokens: Tokens(len(index))}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return m, true, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		m.Files++
		m.DirTokens += Tokens(int(info.Size()))
	}
	return m, true, nil
}

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// ClaudeMemoryDir is where Claude Code keeps the memory of sessions started in
// cwd: <config home>/projects/<cwd with every non-alphanumeric as "-">/memory.
func ClaudeMemoryDir(configHome, cwd string) string {
	return filepath.Join(configHome, "projects", nonAlnum.ReplaceAllString(cwd, "-"), "memory")
}
