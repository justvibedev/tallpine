package sessionartifacts

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// SessionGetter is the one store read Resolve needs when only an id is at hand.
type SessionGetter interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Resolve returns a session's artifact directory: the stored one when the row
// names it (sessions created before artifacts were grouped by project keep
// their flat dir for life), otherwise the canonical Dir. Every reader and writer
// goes through it, so the agent's prompt, the UI and retention agree.
func Resolve(dataDir string, id domain.SessionID, stored string) string {
	if stored = strings.TrimSpace(stored); stored != "" {
		return filepath.Clean(stored)
	}
	return unstoredDir(dataDir, id)
}

// unstoredDir is the directory of a row with no stored ArtifactDir: sessions
// that predate that column (migration 0181 defaulted it to ”) keep their files
// at the flat path, so that wins while it exists as a session dir; everything
// else gets the canonical path.
func unstoredDir(dataDir string, id domain.SessionID) string {
	if legacy := LegacyDir(dataDir, id); legacy != "" {
		if fi, err := os.Stat(legacy); err == nil && fi.IsDir() && !IsProjectFolder(legacy, string(id)) {
			return legacy
		}
	}
	return Dir(dataDir, id)
}

// ResolveManaged is Resolve for code that deletes: a stored dir that is neither
// the canonical nor the legacy flat path (a custom dir) is never followed.
func ResolveManaged(dataDir string, id domain.SessionID, stored string) string {
	if stored = strings.TrimSpace(stored); stored != "" && isManaged(dataDir, id, stored) {
		return filepath.Clean(stored)
	}
	return unstoredDir(dataDir, id)
}

// ResolveForID is Resolve for a caller that has only the id; managed selects
// ResolveManaged. An unknown session, or a failed read, resolves to Dir.
func ResolveForID(ctx context.Context, dataDir string, g SessionGetter, id domain.SessionID, managed bool) string {
	rec, ok, err := g.GetSession(ctx, id)
	if err != nil || !ok {
		return unstoredDir(dataDir, id)
	}
	if managed {
		return ResolveManaged(dataDir, id, rec.Metadata.ArtifactDir)
	}
	return Resolve(dataDir, id, rec.Metadata.ArtifactDir)
}

// IsManaged reports whether a stored ArtifactDir is the session's own managed
// directory (empty, canonical, or legacy flat). Anything else is a custom dir
// that is measured but never archived or deleted.
func IsManaged(dataDir string, id domain.SessionID, stored string) bool {
	return strings.TrimSpace(stored) == "" || isManaged(dataDir, id, stored)
}

func isManaged(dataDir string, id domain.SessionID, stored string) bool {
	c := filepath.Clean(stored)
	return c == filepath.Clean(Dir(dataDir, id)) || c == filepath.Clean(LegacyDir(dataDir, id))
}

var sessionDirName = regexp.MustCompile(`-\d+$`)

// CollidesWithLegacySession reports whether the project folder of id is the
// flat directory of another, legacy session (project "plan-exp-2" next to a
// session "plan-exp-2" of project "plan-exp"). Creating the session's directory
// there would nest it inside that other session's files.
func CollidesWithLegacySession(dataDir string, id domain.SessionID) bool {
	folder := filepath.Join(dataDir, "artifacts", ProjectFolder(id))
	fi, err := os.Lstat(folder)
	if err != nil || !fi.IsDir() || !sessionDirName.MatchString(filepath.Base(folder)) {
		return false
	}
	entries, _ := os.ReadDir(folder)
	return len(entries) > 0 && !IsProjectFolder(folder, filepath.Base(folder))
}

// IsProjectFolder reports whether dir (named name) holds "<name>-<n>" session
// directories, i.e. it is a project folder rather than a session's own files.
func IsProjectFolder(dir, name string) bool {
	subs, _ := os.ReadDir(dir)
	for _, s := range subs {
		n := s.Name()
		if s.IsDir() && len(n) > len(name)+1 && n[:len(name)+1] == name+"-" && sessionDirName.MatchString(n) {
			return true
		}
	}
	return false
}
