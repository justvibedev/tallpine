package contextbudget

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func fakeRead(files map[string]string) Source {
	return Source{Read: func(_ context.Context, p string) ([]byte, bool, error) {
		s, ok := files[p]
		return []byte(s), ok, nil
	}, List: func(_ context.Context, dir string) ([]string, error) {
		var out []string
		for p := range files {
			if strings.HasPrefix(p, dir+"/") {
				out = append(out, p)
			}
		}
		sort.Strings(out)
		return out, nil
	}}
}

func TestMeasureFollowsImportsForClaudeOnly(t *testing.T) {
	files := map[string]string{
		"CLAUDE.md":         "# Rules\nsee @docs/a.md and @docs/a.md again\n",
		"docs/a.md":         "# A\nnested @b.md\n",
		"docs/b.md":         strings.Repeat("x", 300),
		"AGENTS.md":         strings.Repeat("y", 90),
		".claude/CLAUDE.md": "z",
	}
	rep, err := Measure(context.Background(), fakeRead(files), domain.HarnessClaudeCode)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range rep.Files {
		paths = append(paths, f.Path)
	}
	if want := []string{"CLAUDE.md", "docs/a.md", "docs/b.md", ".claude/CLAUDE.md"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("files %v, want %v", paths, want)
	}
	if rep.Tokens < 100 {
		t.Fatalf("tokens %d, want the 300-byte import counted", rep.Tokens)
	}
	codex, _ := Measure(context.Background(), fakeRead(files), domain.HarnessCodex)
	if len(codex.Files) != 1 || codex.Files[0].Path != "AGENTS.md" || codex.Tokens != 30 {
		t.Fatalf("codex report %+v", codex)
	}
}

func TestMeasureImportCycleAndEscapes(t *testing.T) {
	files := map[string]string{
		"CLAUDE.md": "@a.md @../outside.md @/etc/passwd @~/x.md",
		"a.md":      "back to @CLAUDE.md",
	}
	rep, err := Measure(context.Background(), fakeRead(files), domain.HarnessClaudeCode)
	if err != nil || len(rep.Files) != 2 {
		t.Fatalf("files %+v err %v", rep.Files, err)
	}
}

func TestImportsSkipsCodeAndEmails(t *testing.T) {
	md := "ok @docs/x.md.\nmail me@host.com\n`@docs/inline.md`\n```\n@docs/fenced.md\n```\nscoped @types/node\nplain @mention\n"
	got := Imports(md)
	if want := []string{"docs/x.md", "types/node"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("imports %v, want %v", got, want)
	}
}

func TestMeasureBiggestSections(t *testing.T) {
	md := "# Top\nshort\n## Big\n" + strings.Repeat("a", 3000) + "\n```\n# not a heading\n```\n## Mid\n" + strings.Repeat("b", 600) + "\n"
	rep, _ := Measure(context.Background(), fakeRead(map[string]string{"CLAUDE.md": md}), domain.HarnessClaudeCode)
	if len(rep.Sections) != 3 || rep.Sections[0].Heading != "Big" || rep.Sections[1].Heading != "Mid" {
		t.Fatalf("sections %+v", rep.Sections)
	}
}

func TestMeasureMissingFilesIsEmpty(t *testing.T) {
	rep, err := Measure(context.Background(), fakeRead(nil), domain.HarnessClaudeCode)
	if err != nil || rep.Tokens != 0 || len(rep.Files) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestMeasureSkipsUnreadableImport(t *testing.T) {
	src := fakeRead(map[string]string{"CLAUDE.md": "see @backend/internal and @docs/ok.md", "docs/ok.md": "x"})
	inner := src.Read
	src.Read = func(ctx context.Context, p string) ([]byte, bool, error) {
		if p == "backend/internal" {
			return nil, false, errors.New("cat-file blob: not a blob")
		}
		return inner(ctx, p)
	}
	rep, err := Measure(context.Background(), src, domain.HarnessClaudeCode)
	if err != nil || len(rep.Files) != 2 {
		t.Fatalf("files %+v err %v", rep.Files, err)
	}
	// A root file that fails to read still fails the measurement.
	src.Read = func(context.Context, string) ([]byte, bool, error) { return nil, false, errors.New("boom") }
	if _, err := Measure(context.Background(), src, domain.HarnessClaudeCode); err == nil {
		t.Fatal("root read error swallowed")
	}
}

func TestMeasureKiroCountsSteering(t *testing.T) {
	src := fakeRead(map[string]string{
		"AGENTS.md":               "aaa",
		".kiro/steering/a.md":     strings.Repeat("s", 300),
		".kiro/steering/sub/b.md": "bbb",
		".kiro/steering/x.txt":    "ignored",
	})
	rep, err := Measure(context.Background(), src, domain.HarnessKiro)
	if err != nil || len(rep.Files) != 3 || rep.Tokens != 1+100+1 {
		t.Fatalf("report %+v err %v", rep, err)
	}
	if codex, _ := Measure(context.Background(), src, domain.HarnessCodex); len(codex.Files) != 1 {
		t.Fatalf("codex counted steering: %+v", codex.Files)
	}
}

func TestMeasureSkillsCountsNameAndDescriptionOnly(t *testing.T) {
	body := strings.Repeat("x", 9000)
	src := fakeRead(map[string]string{
		".claude/skills/a/SKILL.md": "---\nname: a\ndescription: Does a thing.\nallowed-tools: Read\n---\n" + body,
		".claude/skills/b/SKILL.md": "---\nname: b\ndescription: >\n  Folded\n  text.\n---\n" + body,
		".claude/skills/c/SKILL.md": "no frontmatter " + body,
		".claude/skills/a/notes.md": body,
	})
	got, err := MeasureSkills(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("skills = %+v, want the three SKILL.md files", got)
	}
	for _, f := range got {
		if f.Bytes > 80 {
			t.Fatalf("%s counts %d bytes: the body must not be loaded into the estimate", f.Path, f.Bytes)
		}
	}
	if got[0].Path != ".claude/skills/b/SKILL.md" {
		t.Fatalf("order = %+v, want the biggest description first", got)
	}
}

func TestMeasureMemoryReadsIndexAndSkipsArchive(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := MeasureMemory(dir); ok || err != nil {
		t.Fatalf("empty dir: ok=%v err=%v", ok, err)
	}
	write("MEMORY.md", strings.Repeat("i", 3000))
	write("feedback_a.md", strings.Repeat("f", 600))
	write("archive/old.md", strings.Repeat("o", 9000))
	m, ok, err := MeasureMemory(dir)
	if err != nil || !ok || m.IndexTokens != 1000 || m.Files != 2 || m.DirTokens != 1200 {
		t.Fatalf("memory = %+v ok=%v err=%v", m, ok, err)
	}
	if got := ClaudeMemoryDir("/home/u/.claude", "/Users/me/my.proj"); got != "/home/u/.claude/projects/-Users-me-my-proj/memory" {
		t.Fatalf("memory dir = %s", got)
	}
}
