// Package contextbudget measures what every session of a project loads on
// start (instruction files and their @imports, skills, the Claude orchestrator
// memory, Tallpine's own prompt) once a day, keeps findings about it, and files
// each as an owner card the daemon answers itself (B825). It only reads the
// repository's default branch, never edits it, and never messages an orchestrator.
package contextbudget

import (
	"context"
	"path"
	"regexp"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/gitblob"
)

const (
	// bytesPerToken is a deliberately rough estimate: dense markdown with code
	// runs near 3 bytes per token, so the count errs toward warning.
	bytesPerToken = 3
	maxFileBytes  = 1 << 20
	maxImportDeep = 5
	maxFiles      = 64
	topSections   = 3
)

// Tokens estimates the tokens in n bytes of text.
func Tokens(n int) int { return (n + bytesPerToken - 1) / bytesPerToken }

// ReadFunc returns the file at a repo-relative path on the measured ref, or
// found=false when it does not exist.
type ReadFunc func(ctx context.Context, path string) (data []byte, found bool, err error)

// ListFunc lists the repo-relative paths of every file under dir.
type ListFunc func(ctx context.Context, dir string) ([]string, error)

// Source reads one ref of a repository.
type Source struct {
	Read ReadFunc
	List ListFunc
}

// GitSource reads files of repo at ref with git, never the working tree.
func GitSource(repo, ref string) Source {
	return Source{
		Read: func(ctx context.Context, p string) ([]byte, bool, error) {
			return gitblob.ReadAt(ctx, repo, ref, p, maxFileBytes)
		},
		List: func(ctx context.Context, dir string) ([]string, error) {
			entries, err := gitblob.ListTreeAt(ctx, repo, ref, dir)
			var out []string
			for _, e := range entries {
				if e.Mode == gitblob.ModeRegular || e.Mode == gitblob.ModeExecutable {
					out = append(out, e.Path)
				}
			}
			return out, err
		},
	}
}

// File is one always-loaded file.
type File struct {
	Path   string
	Bytes  int
	Tokens int
}

// Section is one heading's body inside a file.
type Section struct {
	File    string
	Heading string
	Tokens  int
}

// Report is the measurement for one harness.
type Report struct {
	Harness  domain.AgentHarness
	Files    []File
	Sections []Section // biggest first, at most topSections
	Tokens   int
}

// rootFiles are the instruction files a harness loads from the repo root. A
// file that imports others (Claude's @path syntax) pulls them in too.
func rootFiles(h domain.AgentHarness) []string {
	if h == domain.HarnessClaudeCode {
		return []string{"CLAUDE.md", ".claude/CLAUDE.md"}
	}
	return []string{"AGENTS.md"}
}

// steeringDir holds the extra markdown a harness loads from a directory:
// Kiro's agent resources are file://AGENTS.md and file://.kiro/steering/**/*.md
// (adapters/agent/kiro/profile.go). Other harnesses have none. DeepSeek has no
// native instruction-file surface of its own, so AGENTS.md is a best guess.
func steeringDir(h domain.AgentHarness) string {
	if h == domain.HarnessKiro {
		return ".kiro/steering"
	}
	return ""
}

// followsImports reports whether the harness expands @path imports.
func followsImports(h domain.AgentHarness) bool { return h == domain.HarnessClaudeCode }

// Measure sums the always-loaded files for harness h.
func Measure(ctx context.Context, src Source, h domain.AgentHarness) (Report, error) {
	rep := Report{Harness: h}
	seen := map[string]bool{}
	var secs []Section
	var visit func(p string, depth int) error
	visit = func(p string, depth int) error {
		if seen[p] || len(seen) >= maxFiles {
			return nil
		}
		seen[p] = true
		data, ok, err := src.Read(ctx, p)
		if err != nil || !ok {
			// An unreadable import (a directory, say) is skipped; only a root
			// file or a cancelled context fails the measurement.
			if depth > 0 && ctx.Err() == nil {
				return nil
			}
			return err
		}
		rep.Files = append(rep.Files, File{Path: p, Bytes: len(data), Tokens: Tokens(len(data))})
		secs = append(secs, sections(p, string(data))...)
		if !followsImports(h) || depth >= maxImportDeep {
			return nil
		}
		for _, imp := range Imports(string(data)) {
			target := path.Clean(path.Join(path.Dir(p), imp))
			if target == "." || target == ".." || strings.HasPrefix(target, "../") || path.IsAbs(target) {
				continue // outside the repository: not measurable from the default branch
			}
			if err := visit(target, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	roots := rootFiles(h)
	if dir := steeringDir(h); dir != "" && src.List != nil {
		listed, err := src.List(ctx, dir)
		if err != nil {
			return rep, err
		}
		for _, p := range listed {
			if strings.HasSuffix(p, ".md") {
				roots = append(roots, p)
			}
		}
	}
	for _, p := range roots {
		if err := visit(p, 0); err != nil {
			return rep, err
		}
	}
	for _, f := range rep.Files {
		rep.Tokens += f.Tokens
	}
	sort.SliceStable(secs, func(i, j int) bool { return secs[i].Tokens > secs[j].Tokens })
	if len(secs) > topSections {
		secs = secs[:topSections]
	}
	rep.Sections = secs
	return rep, nil
}

// importRe matches Claude Code's @path import: an @ at a word start followed
// by a path. An @ inside a word (an email, a scoped package) is not an import.
var importRe = regexp.MustCompile("(?:^|[\\s(])@([A-Za-z0-9_.][^\\s)`]*)")

// Imports lists the @path imports of a markdown file in order, skipping code
// fences and inline code, and home or absolute paths.
func Imports(md string) []string {
	var out []string
	fenced := false
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range importRe.FindAllStringSubmatch(stripInlineCode(line), -1) {
			p := strings.TrimRight(m[1], ".,;:")
			if strings.Contains(p, "/") || strings.Contains(path.Base(p), ".") {
				out = append(out, p)
			}
		}
	}
	return out
}

func stripInlineCode(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == '`' {
			in = !in
			continue
		}
		if !in {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sections splits md at markdown headings (outside code fences) and sizes
// each body, heading included.
func sections(file, md string) []Section {
	var out []Section
	heading, size, fenced := "(preamble)", 0, false
	flush := func() {
		if size > 0 {
			out = append(out, Section{File: file, Heading: heading, Tokens: Tokens(size)})
		}
	}
	for _, line := range strings.Split(md, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fenced = !fenced
		}
		if !fenced && strings.HasPrefix(line, "#") && strings.HasPrefix(strings.TrimLeft(line, "#"), " ") {
			flush()
			heading, size = strings.TrimSpace(strings.TrimLeft(line, "#")), 0
		}
		size += len(line) + 1
	}
	flush()
	return out
}
