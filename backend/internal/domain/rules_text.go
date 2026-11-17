package domain

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// errEscapesRoot is returned for a configured path that is absolute or climbs
// out of the repository root.
var errEscapesRoot = errors.New("path must be repo-relative and must not escape the project root")

// RepoRelativePath validates rel as a path inside a repository and joins it to
// root. With an empty root it returns the cleaned slash-separated repo path,
// the form git revision specs ("<ref>:<path>") take. Absolute paths and any
// ".." segment are refused; an empty rel returns "" and no error.
func RepoRelativePath(root, rel string) (string, error) {
	trimmed := strings.TrimSpace(rel)
	if trimmed == "" {
		return "", nil
	}
	if filepath.IsAbs(trimmed) || strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, `\`) {
		return "", errEscapesRoot
	}
	clean := filepath.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errEscapesRoot
	}
	for _, seg := range strings.Split(filepath.ToSlash(clean), "/") {
		if seg == ".." {
			return "", errEscapesRoot
		}
	}
	if root == "" {
		return filepath.ToSlash(clean), nil
	}
	return filepath.Join(root, clean), nil
}

// validateRepoRelative refuses paths that would let a project config escape
// its repo root. The same guard runs again where the file is read.
func validateRepoRelative(p string) error {
	_, err := RepoRelativePath("", p)
	return err
}

// CapRules cuts rules longer than MaxAgentRulesBytes at a rune boundary and
// says so inside the text, naming the settings (label) to shorten, so the
// agent and anyone reading the prompt can see the rules are partial. It
// reports whether it cut.
func CapRules(rules, label string) (string, bool) {
	if len(rules) <= MaxAgentRulesBytes {
		return rules, false
	}
	cut := MaxAgentRulesBytes
	for cut > 0 && !utf8.RuneStart(rules[cut]) {
		cut--
	}
	return rules[:cut] + fmt.Sprintf("\n\n[project rules truncated at %d bytes; shorten %s]", MaxAgentRulesBytes, label), true
}
