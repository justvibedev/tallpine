package storageusage

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// roots are the locations a scan looks under.
type roots struct {
	data  string // <data dir>
	state string // the state root (~/.tallpine): running.json's directory
	goDir string // the Go build cache, "" when unknown
	now   func() time.Time
}

// BuildSettleTime is how long a release build must sit untouched before it
// may be removed.
const BuildSettleTime = 10 * time.Minute

func (r roots) rel(p string) string {
	if rel, err := filepath.Rel(r.state, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	if rel, err := filepath.Rel(r.data, p); err == nil && !strings.HasPrefix(rel, "..") {
		return "data/" + filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

// sessionIndex answers who owns a directory named like a session.
type sessionIndex map[domain.SessionID]domain.SessionRecord

func (ix sessionIndex) state(id domain.SessionID) State {
	rec, ok := ix[id]
	switch {
	case !ok:
		return StateOrphaned
	case rec.IsTerminated:
		return StateEnded
	default:
		return StateLive
	}
}

// ownedBySession fills the owner fields and the protection for a directory
// named after a session.
func (ix sessionIndex) ownedBySession(e *Entry, id domain.SessionID, project domain.ProjectID) {
	e.Session, e.Project, e.State = id, project, ix.state(id)
	rec, ok := ix[id]
	if ok {
		e.Project = rec.ProjectID
	}
	switch {
	case ok && !rec.IsTerminated:
		e.Protected = "session is live"
	case ok && rec.Kind == domain.KindOrchestrator:
		e.Protected = "orchestrator session"
	}
}

// liveInProject reports whether a live session belongs to project.
func (ix sessionIndex) liveInProject(project domain.ProjectID) bool {
	for _, rec := range ix {
		if !rec.IsTerminated && rec.ProjectID == project {
			return true
		}
	}
	return false
}

func children(dir string) []os.DirEntry {
	ents, _ := os.ReadDir(dir)
	return ents
}

// plain reports whether the entry is a plain file or directory (a symlink is
// never measured or removed: it may point anywhere).
func plain(e os.DirEntry) bool {
	return e.Type()&os.ModeSymlink == 0 && !strings.HasPrefix(e.Name(), ".lock")
}

// scan lists every location without sizes, protections decided.
func scan(r roots, ix sessionIndex) []Entry {
	var out []Entry
	add := func(cat Category, label, p string) *Entry {
		out = append(out, Entry{ID: string(cat) + ":" + r.rel(p), Category: cat, Label: label, Path: p})
		return &out[len(out)-1]
	}
	each := func(dir string, fn func(os.DirEntry, string)) {
		for _, e := range children(dir) {
			if plain(e) {
				fn(e, filepath.Join(dir, e.Name()))
			}
		}
	}

	// Worktrees: <data>/worktrees/<project>/<session>.
	each(filepath.Join(r.data, "worktrees"), func(pe os.DirEntry, pdir string) {
		if !pe.IsDir() {
			return
		}
		each(pdir, func(se os.DirEntry, sdir string) {
			if se.IsDir() {
				e := add(CategoryWorktrees, "worktree", sdir)
				ix.ownedBySession(e, domain.SessionID(se.Name()), domain.ProjectID(pe.Name()))
			}
		})
	})
	// Session scratch: <data>/tmp/<session>; .trash holds discarded ones.
	each(filepath.Join(r.data, sessionscratch.TempRoot), func(se os.DirEntry, p string) {
		if !se.IsDir() {
			return
		}
		if se.Name() == sessionscratch.TrashDir {
			add(CategorySessionTemp, "discarded scratch (being removed)", p)
			return
		}
		ix.ownedBySession(add(CategorySessionTemp, "session scratch", p), domain.SessionID(se.Name()), "")
	})
	// Artifacts and attachments per session, where the name is one.
	each(filepath.Join(r.data, "artifacts"), func(se os.DirEntry, p string) {
		e := add(CategoryArtifacts, "session artifacts", p)
		if _, ok := ix[domain.SessionID(se.Name())]; ok {
			ix.ownedBySession(e, domain.SessionID(se.Name()), "")
		}
		e.Protected = "managed by artifact retention (use Clean up)"
	})
	each(filepath.Join(r.data, "artifacts-trash"), func(_ os.DirEntry, p string) {
		add(CategoryArtifacts, "discarded artifacts", p)
	})
	each(filepath.Join(r.data, "attachments"), func(se os.DirEntry, p string) {
		e := add(CategoryAttachments, "attachments", p)
		if _, ok := ix[domain.SessionID(se.Name())]; ok {
			ix.ownedBySession(e, domain.SessionID(se.Name()), "")
		}
		e.Protected = "referenced by conversations; files of ended sessions are removed after 14 days"
	})
	// Salvaged work: <data>/salvage/<session>/<stamp>.
	each(filepath.Join(r.data, "salvage"), func(se os.DirEntry, sdir string) {
		each(sdir, func(_ os.DirEntry, p string) {
			e := add(CategorySalvage, "salvaged work (patch + files)", p)
			e.Session, e.State = domain.SessionID(se.Name()), ix.state(domain.SessionID(se.Name()))
			e.Project = ix[e.Session].ProjectID
		})
	})
	// Harness caches: <data>/caches/<kind>/<project>.
	each(filepath.Join(r.data, sessionscratch.CacheRoot), func(ke os.DirEntry, kdir string) {
		if ke.Name() == sessionscratch.CacheTrashDir {
			add(CategoryCaches, "discarded caches (being removed)", kdir)
			return
		}
		each(kdir, func(pe os.DirEntry, p string) {
			project := domain.ProjectID(pe.Name())
			if pe.Name() == sessionscratch.StandaloneGradleHome {
				project = ""
			}
			e := add(CategoryCaches, ke.Name()+" cache", p)
			e.Project = project
			if ix.liveInProject(project) {
				e.Protected = "a session in this project is live"
			}
		})
	})
	if r.goDir != "" {
		add(CategoryGoBuildCache, "Go build cache (shared by agents)", r.goDir).Protected = "trimmed by Clean up; entries unused for 2 days"
	}
	// Releases: every directory is a rollback point; the newest stays.
	// release-builds are named by commit, so they order by modification time
	// (as release-local does); a build touched in the last few minutes may
	// still be running and is left alone.
	rollback := func(cat Category, dir, label string, byMTime bool) {
		type dated struct {
			name string
			at   time.Time
		}
		var dirs []dated
		for _, e := range children(dir) {
			if plain(e) && e.IsDir() && !strings.HasPrefix(e.Name(), ".") && !strings.HasSuffix(e.Name(), ".partial") && e.Name() != "rolled-back" {
				d := dated{name: e.Name()}
				if info, err := e.Info(); err == nil {
					d.at = info.ModTime()
				}
				dirs = append(dirs, d)
			}
		}
		sort.Slice(dirs, func(i, j int) bool { // oldest first
			if byMTime && !dirs[i].at.Equal(dirs[j].at) {
				return dirs[i].at.Before(dirs[j].at)
			}
			return dirs[i].name < dirs[j].name
		})
		for i, d := range dirs {
			e := add(cat, label, filepath.Join(dir, d.name))
			switch {
			case i == len(dirs)-1:
				e.Protected = "newest " + label + " is kept"
			case byMTime && r.now().Sub(d.at) < BuildSettleTime:
				e.Protected = label + " changed in the last few minutes; it may still be building"
			}
		}
	}
	rollback(CategoryReleases, filepath.Join(r.state, "releases"), "rollback point", false)
	each(filepath.Join(r.state, "releases", "rolled-back"), func(_ os.DirEntry, p string) {
		add(CategoryReleases, "replaced by a rollback", p)
	})
	rollback(CategoryReleaseBuild, filepath.Join(r.state, "release-builds"), "release build", true)
	// The database: one entry (db + wal + shm), never removable here.
	add(CategoryDatabase, "App database", filepath.Join(r.data, "ao.db")).Protected = "the app's database"
	each(filepath.Join(r.data, "logs"), func(fe os.DirEntry, p string) {
		e := add(CategoryLogs, "log", p)
		if fe.Name() == "daemon.log" {
			e.Protected = "the daemon's current log"
		}
	})
	each(filepath.Join(r.state, "exports"), func(_ os.DirEntry, p string) { add(CategoryExports, "export", p) })
	each(filepath.Join(r.state, "backups"), func(_ os.DirEntry, p string) { add(CategoryBackups, "backup", p) })
	each(filepath.Join(r.state, "build-cache"), func(_ os.DirEntry, p string) { add(CategoryBuildCache, "build cache", p) })
	each(filepath.Join(r.state, "local-ci"), func(_ os.DirEntry, p string) { add(CategoryLocalCI, "local CI cache", p) })
	return out
}
