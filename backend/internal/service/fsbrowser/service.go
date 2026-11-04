// Package fsbrowser provides read-only directory listings for remote clients.
package fsbrowser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

const maxEntries = 500

// Entry is one visible subdirectory.
type Entry struct {
	Name    string
	Path    string
	GitRepo bool
}

// Listing is a bounded directory listing on the daemon host.
type Listing struct {
	Path      string
	Parent    string
	Entries   []Entry
	Truncated bool
}

// Service browses the daemon host's filesystem without exposing file contents.
type Service struct{}

// New constructs a directory browser backed by the host filesystem.
func New() *Service { return &Service{} }

// List returns the visible subdirectories of path. An empty path uses the
// daemon user's home directory.
func (*Service) List(ctx context.Context, path string) (Listing, error) {
	if err := ctx.Err(); err != nil {
		return Listing{}, err
	}
	home, _ := os.UserHomeDir()
	if path == "" {
		if home == "" {
			return Listing{}, errors.New("no home directory")
		}
		path = home
	}
	if !filepath.IsAbs(path) {
		return Listing{}, apierr.Invalid("FS_PATH_NOT_ABSOLUTE", "path must be absolute on the daemon host", nil)
	}
	path = filepath.Clean(path)
	if blockedPath(home, path) {
		return Listing{}, apierr.Forbidden("FS_FORBIDDEN", "that folder is not browsable")
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return Listing{}, apierr.NotFound("FS_NOT_FOUND", "no such directory on the daemon host")
		case errors.Is(err, os.ErrPermission):
			return Listing{}, apierr.Forbidden("FS_FORBIDDEN", "the daemon may not read that directory")
		default:
			return Listing{}, apierr.Invalid("FS_NOT_A_DIRECTORY", "not a directory", nil)
		}
	}

	out := Listing{Path: path, Parent: filepath.Dir(path), Entries: []Entry{}}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if len(out.Entries) == maxEntries {
			out.Truncated = true
			break
		}
		child := filepath.Join(path, entry.Name())
		if blockedPath(home, child) {
			continue
		}
		if probeSkipped(home, child) {
			out.Entries = append(out.Entries, Entry{Name: entry.Name(), Path: child})
			continue
		}
		// .git may be a directory (normal clone) or a file (worktree): both are repos.
		_, gitErr := os.Stat(filepath.Join(child, ".git"))
		out.Entries = append(out.Entries, Entry{Name: entry.Name(), Path: child, GitRepo: gitErr == nil})
	}
	return out, nil
}

// Folders macOS guards with privacy prompts, or that sync with the cloud. The
// picker must not look inside them on its own: a stat of <folder>/.git makes
// the OS ask the user for access to Documents, Desktop, Downloads or Music
// every time the home listing opens.
var (
	// Never listed, never entered.
	blockedHomeFolders = []string{"Music", "Movies", "Pictures", "Library/Mobile Documents", "Library/CloudStorage"}
	// Listed (and enterable on a click) but not probed for a .git from home.
	unprobedHomeFolders = []string{"Desktop", "Documents", "Downloads", "Public", "Library"}
)

func underHome(home, path string, folders []string, descend bool) bool {
	if home == "" {
		return false
	}
	for _, folder := range folders {
		blocked := filepath.Join(home, folder)
		if strings.EqualFold(path, blocked) {
			return true
		}
		if descend && len(path) > len(blocked) && strings.EqualFold(path[:len(blocked)], blocked) && path[len(blocked)] == filepath.Separator {
			return true
		}
	}
	return false
}

func blockedPath(home, path string) bool { return underHome(home, path, blockedHomeFolders, true) }

func probeSkipped(home, path string) bool { return underHome(home, path, unprobedHomeFolders, false) }
