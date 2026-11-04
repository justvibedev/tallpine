// Package sessionscratch owns the scratch space Tallpine hands every agent session:
// a private temp directory (exported as TMPDIR, TMP and TEMP) and a
// per-project Gradle user home. Both live under the data directory, so what an
// agent leaves behind is attributable to a session or project and Tallpine can
// remove it, instead of piling up in the system temp directory or in ad-hoc
// homes such as ~/.gradle-<harness>.
//
// A session's temp directory is discarded when the session ends: it is renamed
// into a trash directory (one rename, so teardown never waits on a large tree)
// and removed from there afterwards.
package sessionscratch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/fsperm"
)

const (
	// TempRoot holds one temp directory per session, relative to the data
	// directory.
	TempRoot = "tmp"
	// TrashDir holds discarded session temp directories until they are
	// removed, relative to TempRoot. The leading dot keeps it from ever
	// matching a session id.
	TrashDir = ".trash"
	// CacheRoot holds harness caches Tallpine points agents at, relative to the
	// data directory.
	CacheRoot = "caches"
	// GradleDir holds one Gradle user home per project, relative to CacheRoot.
	GradleDir = "gradle"
	// StandaloneGradleHome is the Gradle home of sessions without a project.
	StandaloneGradleHome = "_standalone"
	// CacheTrashDir holds discarded harness caches until they are removed,
	// relative to CacheRoot.
	CacheTrashDir = ".trash"
)

// gradleSharedEntries are files in the user's own Gradle home that carry
// configuration rather than cache: credentials, signing properties and init
// scripts. A project home links to them so a build that needs them still
// finds them.
var gradleSharedEntries = []string{"gradle.properties", "init.d", "init.gradle", "init.gradle.kts"}

// segment reports whether name is safe as a single path element: not empty,
// not a dot entry, no separators, no NUL, no leading dot.
func segment(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || len(name) > 255 {
		return false
	}
	return !strings.ContainsAny(name, `/\`+"\x00")
}

// TempDir returns the session's temp directory, or "" when dataDir is unset
// or the id is not a safe path element.
func TempDir(dataDir string, id domain.SessionID) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" || !segment(string(id)) {
		return ""
	}
	return filepath.Join(dataDir, TempRoot, string(id))
}

// TempPrefixEnv is zsh's temp-file prefix. zsh builds here-document and =(...)
// files at $TMPPREFIX plus a suffix and ignores TMPDIR, so its /tmp/zsh default
// is denied inside the Security mode fence, which allows only the session's
// temp dir (B521).
const TempPrefixEnv = "TMPPREFIX"

// TempPrefix is the TMPPREFIX value that keeps zsh's temp files in dir.
func TempPrefix(dir string) string { return filepath.Join(dir, "zsh") }

// GradleHome returns the project's Gradle user home, or "" when dataDir is
// unset or the project id is not a safe path element. Sessions without a
// project share StandaloneGradleHome.
func GradleHome(dataDir string, project domain.ProjectID) string {
	dataDir = strings.TrimSpace(dataDir)
	name := string(project)
	if name == "" {
		name = StandaloneGradleHome
	} else if !segment(name) {
		return ""
	}
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, CacheRoot, GradleDir, name)
}

// EnsureTempDir creates the session's temp directory (mode 0700) and returns
// its path.
func EnsureTempDir(dataDir string, id domain.SessionID) (string, error) {
	dir := TempDir(dataDir, id)
	if dir == "" {
		return "", fmt.Errorf("session temp dir: invalid session %q", id)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// EnsureGradleHome creates the project's Gradle user home and links the
// configuration entries of userGradleHome (normally ~/.gradle) into it, so
// signing and repository credentials keep working. An entry the project home
// already has, as a link or a real file, is left alone. An empty
// userGradleHome links nothing.
func EnsureGradleHome(dataDir string, project domain.ProjectID, userGradleHome string) (string, error) {
	home := GradleHome(dataDir, project)
	if home == "" {
		return "", fmt.Errorf("gradle home: invalid project %q", project)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", err
	}
	if userGradleHome = strings.TrimSpace(userGradleHome); userGradleHome == "" {
		return home, nil
	}
	for _, name := range gradleSharedEntries {
		source := filepath.Join(userGradleHome, name)
		if _, err := os.Stat(source); err != nil {
			continue
		}
		target := filepath.Join(home, name)
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Symlink(source, target); err != nil && !errors.Is(err, fs.ErrExist) {
			return home, fmt.Errorf("gradle home: link %s: %w", name, err)
		}
	}
	return home, nil
}

// Discard moves the session's temp directory into the trash and returns the
// trash path, or "" when there was nothing to move. The caller removes the
// returned path with RemoveTree, typically off the teardown path.
func Discard(dataDir string, id domain.SessionID, now time.Time) (string, error) {
	dir := TempDir(dataDir, id)
	if dir == "" {
		return "", nil
	}
	return discardInto(dir, filepath.Join(dataDir, TempRoot, TrashDir), fmt.Sprintf("%s-%d", id, now.UnixNano()))
}

// DiscardGradleHome moves the project's Gradle home into the cache trash and
// returns the trash path, or "" when there was nothing to move. The caller
// removes the returned tree; EmptyCacheTrash removes what is left. One rename,
// so a session that starts meanwhile gets a fresh home instead of one being
// removed underneath it.
func DiscardGradleHome(dataDir string, project domain.ProjectID, now time.Time) (string, error) {
	home := GradleHome(dataDir, project)
	if home == "" {
		return "", fmt.Errorf("gradle home: invalid project %q", project)
	}
	name := fmt.Sprintf("%s-%s-%d", GradleDir, filepath.Base(home), now.UnixNano())
	return discardInto(home, filepath.Join(dataDir, CacheRoot, CacheTrashDir), name)
}

// discardInto renames dir to trash/name, creating trash first.
func discardInto(dir, trash, name string) (string, error) {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return "", err
	}
	moved := filepath.Join(trash, name)
	if err := os.Rename(dir, moved); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return moved, nil
}

// EmptyTrash removes every discarded temp directory. Entries a removal cannot
// finish stay for the next call.
func EmptyTrash(ctx context.Context, dataDir string) error {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil
	}
	return emptyDir(ctx, filepath.Join(dataDir, TempRoot, TrashDir))
}

// EmptyCacheTrash removes every discarded harness cache. Entries a removal
// cannot finish stay for the next call.
func EmptyCacheTrash(ctx context.Context, dataDir string) error {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil
	}
	return emptyDir(ctx, filepath.Join(dataDir, CacheRoot, CacheTrashDir))
}

// emptyDir removes every entry of dir, keeping dir itself.
func emptyDir(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := RemoveTree(filepath.Join(dir, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// RemoveTree removes path even when its directories were left without owner
// write permission; see fsperm.RemoveAllRepairing.
func RemoveTree(path string) error { return fsperm.RemoveAllRepairing(path) }
