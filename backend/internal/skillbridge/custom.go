package skillbridge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/hookutil"
)

// ScopeCustom is an owner-managed skill under the Tallpine data dir. Tallpine
// lists the enabled ones in each session's prompt; no harness loads them on
// its own.
const ScopeCustom Scope = "custom"

// ConvCustom is the custom skills directory family of a project;
// ConvCustomApp is the app-wide one (B740 a13), which every project sees.
const (
	ConvCustom    Convention = "custom"
	ConvCustomApp Convention = "custom-app"
)

// Owner-only modes of the custom skills tree.
const (
	customDirMode  os.FileMode = 0o700
	customFileMode os.FileMode = 0o600
)

// ErrSkillExists is returned by Create and Import for a name already in use.
var ErrSkillExists = errors.New("a custom skill with this name already exists")

// CustomDir is project id's custom skills directory,
// <dataDir>/projects/<id>/skills, or "" when either is unusable.
func CustomDir(dataDir, id string) string {
	if strings.TrimSpace(dataDir) == "" || id == "" || !filepath.IsLocal(id) || strings.ContainsAny(id, `/\`) {
		return ""
	}
	return filepath.Join(dataDir, "projects", id, "skills")
}

// AppCustomDir is the app-wide custom skills directory,
// <dataDir>/skills-custom, or "" without a data dir. It is never
// <dataDir>/skills, which the daemon rewrites at boot.
func AppCustomDir(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return ""
	}
	return filepath.Join(dataDir, "skills-custom")
}

// CustomStore manages the custom skills in Root, one directory per skill
// holding SKILL.md and optional Markdown references. Every directory is 0700
// and every file 0600.
type CustomStore struct {
	Root string
}

// CustomSkill is one stored custom skill.
type CustomSkill struct {
	Name        string
	Description string
	Path        string
	// Content is the SKILL.md text.
	Content string
	// Files lists the skill's other Markdown files, relative and sorted.
	Files []string
}

func (c CustomStore) dir(name string) (string, error) {
	if c.Root == "" {
		return "", errors.New("custom skills directory is not configured")
	}
	if err := ValidSkillName(name); err != nil {
		return "", err
	}
	return filepath.Join(c.Root, name), nil
}

// List returns every stored custom skill, sorted by name. A missing root is
// an empty list.
func (c CustomStore) List() ([]CustomSkill, error) {
	entries, err := os.ReadDir(c.Root)
	if errors.Is(err, fs.ErrNotExist) || c.Root == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []CustomSkill
	for _, e := range entries {
		if !e.IsDir() || ValidSkillName(e.Name()) != nil {
			continue
		}
		if s, err := c.Get(e.Name()); err == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// Get reads custom skill name; fs.ErrNotExist when it is not stored.
func (c CustomStore) Get(name string) (CustomSkill, error) {
	dir, err := c.dir(name)
	if err != nil {
		return CustomSkill{}, err
	}
	text, _, err := readTextDir(dir, name, domain.SkillSourceCustom)
	if err != nil {
		return CustomSkill{}, err
	}
	out := CustomSkill{Name: name, Description: text.Description, Path: dir, Files: []string{}}
	for _, f := range text.Files {
		if f.Rel == "SKILL.md" {
			out.Content = string(f.Data)
		} else {
			out.Files = append(out.Files, f.Rel)
		}
	}
	return out, nil
}

// Create stores a new skill whose SKILL.md is content.
func (c CustomStore) Create(name, content string) (CustomSkill, error) {
	if err := checkSkillMD(content); err != nil {
		return CustomSkill{}, err
	}
	return c.write(name, []TextFile{{Rel: "SKILL.md", Data: []byte(content)}})
}

// Update replaces the SKILL.md of an existing skill and keeps its references.
func (c CustomStore) Update(name, content string) (CustomSkill, error) {
	if err := checkSkillMD(content); err != nil {
		return CustomSkill{}, err
	}
	dir, err := c.dir(name)
	if err != nil {
		return CustomSkill{}, err
	}
	if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
		return CustomSkill{}, fs.ErrNotExist
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), customFileMode); err != nil {
		return CustomSkill{}, err
	}
	return c.Get(name)
}

// Import copies the skill directory src (SKILL.md plus Markdown references)
// as custom skill name, text-only: symlinks, executables and other files
// inside it are skipped and returned (src itself may be a link). src must hold a SKILL.md and fit MaxTextSkillBytes.
func (c CustomStore) Import(name, src string) (CustomSkill, []string, error) {
	if _, err := c.dir(name); err != nil {
		return CustomSkill{}, nil, err
	}
	src = filepath.Clean(src)
	if !filepath.IsAbs(src) {
		return CustomSkill{}, nil, errors.New("import source must be an absolute path")
	}
	// The folder the owner picked may itself be a link (account skills often
	// are); resolve it once. Links inside it are still never followed.
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return CustomSkill{}, nil, fmt.Errorf("import source %s is not a directory", src)
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return CustomSkill{}, nil, fmt.Errorf("import source %s is not a directory", src)
	}
	text, skipped, err := readTextDir(root, name, domain.SkillSourceCustom)
	if err != nil {
		return CustomSkill{}, skipped, err
	}
	if !slices.ContainsFunc(text.Files, func(f TextFile) bool { return f.Rel == "SKILL.md" }) {
		return CustomSkill{}, skipped, fmt.Errorf("import source %s has no SKILL.md", src)
	}
	if size := text.Size(); size > MaxTextSkillBytes {
		return CustomSkill{}, skipped, fmt.Errorf("skill is %d bytes, over the %d KiB limit", size, MaxTextSkillBytes>>10)
	}
	out, err := c.write(name, text.Files)
	return out, skipped, err
}

// Delete removes custom skill name.
func (c CustomStore) Delete(name string) error {
	dir, err := c.dir(name)
	if err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return fs.ErrNotExist
	}
	if !st.IsDir() {
		return os.Remove(dir) // a stray symlink or file: remove the entry, never its target
	}
	return os.RemoveAll(dir)
}

// write creates skill name from files in a staging directory and renames it
// into place, so a half-written skill is never visible.
func (c CustomStore) write(name string, files []TextFile) (CustomSkill, error) {
	dir, err := c.dir(name)
	if err != nil {
		return CustomSkill{}, err
	}
	if _, err := os.Lstat(dir); err == nil {
		return CustomSkill{}, ErrSkillExists
	}
	if err := os.MkdirAll(c.Root, customDirMode); err != nil {
		return CustomSkill{}, err
	}
	if err := os.Chmod(c.Root, customDirMode); err != nil {
		return CustomSkill{}, err
	}
	stage, err := os.MkdirTemp(c.Root, ".stage-"+name+"-")
	if err != nil {
		return CustomSkill{}, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := WriteTextFiles(stage, files); err != nil {
		return CustomSkill{}, err
	}
	if err := os.Rename(stage, dir); err != nil {
		return CustomSkill{}, err
	}
	return c.Get(name)
}

// WriteTextFiles writes files under dir with owner-only modes (dirs 0700,
// files 0600). Each Rel must be a clean local path (textFileRel).
func WriteTextFiles(dir string, files []TextFile) error {
	if err := os.MkdirAll(dir, customDirMode); err != nil {
		return err
	}
	if err := os.Chmod(dir, customDirMode); err != nil {
		return err
	}
	for _, f := range files {
		rel := textFileRel(f.Rel)
		if rel == "" {
			return fmt.Errorf("unsafe skill file path %q", f.Rel)
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), customDirMode); err != nil {
			return err
		}
		if err := os.WriteFile(dst, f.Data, customFileMode); err != nil {
			return err
		}
		if err := os.Chmod(dst, customFileMode); err != nil {
			return err
		}
	}
	return nil
}

// checkSkillMD validates SKILL.md text typed in the app.
func checkSkillMD(content string) error {
	switch {
	case strings.TrimSpace(content) == "":
		return errors.New("SKILL.md content is required")
	case len(content) > MaxTextSkillBytes:
		return fmt.Errorf("SKILL.md is %d bytes, over the %d KiB limit", len(content), MaxTextSkillBytes>>10)
	case !isText([]byte(content)):
		return errors.New("SKILL.md must be UTF-8 text")
	}
	return nil
}

// readTextDir reads the text-only copy of the skill directory dir: SKILL.md
// and other regular, non-executable .md files at local dot-free paths. It
// never follows a symlink. Every left-out file is returned as a skip note.
func readTextDir(dir, name string, src domain.SkillSource) (TextSkill, []string, error) {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return TextSkill{}, nil, fs.ErrNotExist
	}
	out := TextSkill{Name: name, Source: src}
	var skipped []string
	var total int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		relOS, _ := filepath.Rel(dir, p)
		rel := filepath.ToSlash(relOS)
		if d.IsDir() {
			if textFileRel(rel) == "" {
				return fs.SkipDir
			}
			return nil
		}
		reason := ""
		info, ierr := d.Info()
		switch {
		case ierr != nil:
			reason = skipNotRegular
		case info.Mode()&fs.ModeSymlink != 0:
			reason = skipSymlink
		case !info.Mode().IsRegular():
			reason = skipNotRegular
		case info.Mode().Perm()&0o111 != 0:
			reason = skipExecutable
		case textFileRel(rel) == "":
			reason = skipBadPath
		case !isMarkdown(rel):
			reason = skipNotMarkdown
		case total+info.Size() > MaxTextSkillBytes:
			return fmt.Errorf("skill %q is over the %d KiB limit", name, MaxTextSkillBytes>>10)
		}
		if reason != "" {
			skipped = append(skipped, skippedFile(src, name, rel, reason))
			return nil
		}
		data, err := os.ReadFile(p) //nolint:gosec // a regular file found under the skill directory
		if err != nil {
			return err
		}
		if !isText(data) {
			skipped = append(skipped, skippedFile(src, name, rel, skipNotText))
			return nil
		}
		total += int64(len(data))
		out.Files = append(out.Files, TextFile{Rel: rel, Data: data})
		if rel == "SKILL.md" {
			_, out.Description = frontmatterOf(data)
		}
		return nil
	})
	if err != nil {
		return TextSkill{}, skipped, err
	}
	slices.SortFunc(out.Files, func(a, b TextFile) int { return strings.Compare(a.Rel, b.Rel) })
	return out, skipped, nil
}

// ReadCustomText is the text-only copy of enabled custom skills for one
// reviewer pass, taken from budget in name order; want filters by name.
func (c CustomStore) ReadCustomText(want func(name string) bool, budget *TextBudget) ([]TextSkill, []string) {
	list, err := c.List()
	if err != nil {
		return nil, []string{fmt.Sprintf("custom skills could not be read: %v", err)}
	}
	var out []TextSkill
	var skipped []string
	for _, s := range list {
		if want != nil && !want(s.Name) {
			continue
		}
		dir, _ := c.dir(s.Name)
		text, notes, err := readTextDir(dir, s.Name, domain.SkillSourceCustom)
		skipped = append(skipped, notes...)
		if err != nil {
			skipped = append(skipped, skippedSkill(domain.SkillSourceCustom, s.Name, err.Error()))
			continue
		}
		if reason := budget.take(text.Size()); reason != "" {
			skipped = append(skipped, skippedSkill(domain.SkillSourceCustom, s.Name, reason))
			continue
		}
		out = append(out, text)
	}
	return out, skipped
}
