package skillbridge

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Caps on the text-only skill copies a reviewer pass gets (B740 n1). A skill
// over MaxTextSkillBytes, or one that would take the pass past
// MaxTextSkillsBytes or MaxTextSkills, is skipped and named in the prompt.
const (
	MaxTextSkillBytes  = 64 << 10
	MaxTextSkillsBytes = 256 << 10
	MaxTextSkills      = 20
)

// TextFile is one Markdown file of a text-only skill copy. Rel is
// slash-separated and local to the skill directory.
type TextFile struct {
	Rel  string
	Data []byte
}

// TextSkill is a skill reduced to its SKILL.md and Markdown references: no
// scripts, no executable bits, no symlinks.
type TextSkill struct {
	Name        string
	Description string
	Source      domain.SkillSource
	Files       []TextFile
}

// Size is the skill's total file bytes.
func (s TextSkill) Size() int64 {
	var n int64
	for _, f := range s.Files {
		n += int64(len(f.Data))
	}
	return n
}

// skillNamePattern is a skill directory name that stays one path segment.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidSkillName refuses a name that is not one lowercase path segment, so a
// name can never escape the directory its skill is stored or copied in.
func ValidSkillName(name string) error {
	if !skillNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("skill name %q must be 1-64 characters of a-z, 0-9, '.', '_' or '-', start with a letter or digit and not contain '..'", name)
	}
	return nil
}

// Why a file of a skill directory is left out of its text-only copy.
const (
	skipSymlink     = "symlink"
	skipExecutable  = "executable"
	skipNotMarkdown = "not a .md file"
	skipNotRegular  = "not a regular file"
	skipBadPath     = "unsafe path"
	skipNotText     = "not UTF-8 text"
)

// textFileRel cleans a file path relative to its skill directory, or "" when
// it is not a local, dot-free relative path.
func textFileRel(rel string) string {
	rel = path.Clean(strings.TrimPrefix(rel, "./"))
	if rel == "." || strings.HasPrefix(rel, "/") || rel == ".." || strings.HasPrefix(rel, "../") {
		return ""
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") || strings.ContainsAny(seg, "\\\x00") {
			return ""
		}
	}
	return rel
}

func isMarkdown(rel string) bool { return strings.EqualFold(path.Ext(rel), ".md") }

func isText(b []byte) bool { return utf8.Valid(b) && !strings.ContainsRune(string(b), 0) }

// TextBudget is what is left of a reviewer pass's skill caps.
type TextBudget struct {
	Skills int
	Bytes  int64
}

// NewTextBudget is the full n1 budget of one pass.
func NewTextBudget() *TextBudget {
	return &TextBudget{Skills: MaxTextSkills, Bytes: MaxTextSkillsBytes}
}

// fits reports why a skill of size bytes does not fit, or "" when it does.
func (b *TextBudget) fits(size int64) string {
	switch {
	case size > MaxTextSkillBytes:
		return fmt.Sprintf("over the %d KiB per-skill limit (%d bytes)", MaxTextSkillBytes>>10, size)
	case b.Skills <= 0:
		return fmt.Sprintf("over the %d-skill limit", MaxTextSkills)
	case size > b.Bytes:
		return fmt.Sprintf("over the %d KiB total limit", MaxTextSkillsBytes>>10)
	}
	return ""
}

// take reserves a skill of size bytes, or returns why it does not fit. Call
// it only for a skill that is delivered, so a dropped one costs nothing.
func (b *TextBudget) take(size int64) string {
	if reason := b.fits(size); reason != "" {
		return reason
	}
	b.Skills--
	b.Bytes -= size
	return ""
}

func skippedSkill(src domain.SkillSource, name, reason string) string {
	return fmt.Sprintf("%s skill %q: %s", src, name, reason)
}

func skippedFile(src domain.SkillSource, name, rel, reason string) string {
	return fmt.Sprintf("%s skill %q file %q: %s", src, name, rel, reason)
}
