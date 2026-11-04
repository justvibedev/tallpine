// Package artifactstorage reports how much disk each session's artifact
// directory uses and applies a user-confirmed retention action (archive or
// delete) to the artifacts of sessions terminated a while ago.
//
// Nothing here runs on its own: a deliverable is only ever archived or deleted
// by an explicit Retain call, and only for a session that is terminated and
// older than the requested window.
package artifactstorage

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

const (
	// DefaultWarnBytes is the per-session artifact size above which a session is
	// flagged (1 GiB).
	DefaultWarnBytes int64 = 1 << 30
	// DefaultOlderThanDays is the default age window for retention candidates.
	DefaultOlderThanDays = 30
	// MaxOlderThanDays bounds the age window a client may request.
	MaxOlderThanDays = 3650
	// ArchiveDir holds archives, relative to the data directory.
	ArchiveDir = "artifacts-archive"
)

// Action is a retention action.
type Action string

const (
	// ActionArchive keeps a session's deliverable files as one compressed
	// archive and removes the live copy, version history, and tool directories.
	ActionArchive Action = "archive"
	// ActionDelete removes the session's artifact files and version history.
	ActionDelete Action = "delete"
)

// Valid reports whether a is a known action.
func (a Action) Valid() bool { return a == ActionArchive || a == ActionDelete }

// Status is the outcome of a retention action on one session.
type Status string

// Retention outcomes reported per session.
const (
	StatusArchived Status = "archived"
	StatusDeleted  Status = "deleted"
	StatusSkipped  Status = "skipped"
	StatusFailed   Status = "failed"
)

// ErrInvalid reports a request the service refuses (unknown action, bad
// window, or no sessions).
var ErrInvalid = errors.New("invalid artifact retention request")

// SessionSource lists the sessions whose artifacts are measured.
type SessionSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// Deleter removes a session's artifact files and version history, leaving an
// empty artifact directory behind.
type Deleter interface {
	DeleteSession(ctx context.Context, id domain.SessionID) error
}

// Protector reports what live (inbox or open) backlog items point at, so the
// directories they rely on are never archived or deleted. sessions are linked
// session ids, artifacts are linked artifact refs, texts are item notes where
// design docs are referenced by path (agent-orchestrator-494/design.md).
type Protector interface {
	LiveBacklogArtifactRefs(ctx context.Context) (sessions, artifacts, texts []string, err error)
}

// ProtectedReason explains why a terminated session's artifacts are kept.
const ProtectedReason = "referenced by an open backlog item"

// Entry is one session's artifact usage.
type Entry struct {
	SessionID     domain.SessionID
	ProjectID     domain.ProjectID
	DisplayName   string
	Terminated    bool
	UpdatedAt     time.Time
	SizeBytes     int64
	VersionsBytes int64
	Files         int
	// OverWarn marks a session at or above the warning threshold.
	OverWarn bool
	// Eligible marks a terminated session older than the requested window that
	// no live backlog item references.
	Eligible bool
	// Protected marks a session whose artifacts a live backlog item references.
	Protected bool
}

// Report is the artifact usage across sessions, largest first.
type Report struct {
	OlderThanDays int
	WarnBytes     int64
	TotalBytes    int64
	VersionsBytes int64
	EligibleBytes int64
	Entries       []Entry
}

// Result is the outcome of a retention action on one session.
type Result struct {
	SessionID   domain.SessionID
	Status      Status
	Reason      string
	FreedBytes  int64
	ArchivePath string
}

// Service measures and trims session artifact directories.
type Service struct {
	dataDir   string
	sessions  SessionSource
	deleter   Deleter
	warnBytes int64
	protector Protector
	now       func() time.Time
	// mu serializes retention runs so two requests cannot archive one session
	// at the same time.
	mu sync.Mutex
}

// New builds a Service. A warnBytes of zero or less uses DefaultWarnBytes.
func New(dataDir string, sessions SessionSource, deleter Deleter, warnBytes int64) *Service {
	if warnBytes <= 0 {
		warnBytes = DefaultWarnBytes
	}
	return &Service{dataDir: dataDir, sessions: sessions, deleter: deleter, warnBytes: warnBytes, now: time.Now}
}

// SetProtector makes the service skip sessions that live backlog items
// reference. Without one nothing is protected.
func (s *Service) SetProtector(p Protector) { s.protector = p }

// Protected returns the session ids a live backlog item references, read once
// so a caller (the cleanup rule) can decide for many sessions. Failing to read
// them is an error: a run never proceeds blind.
func (s *Service) Protected(ctx context.Context) (map[domain.SessionID]bool, error) {
	return s.protectedIDs(ctx)
}

// protectedIDs returns the session ids a live backlog item references: linked
// sessions, plus any "<id>/" path in an artifact link or an item's text.
// Failing to read them is an error, so a retention run never proceeds blind.
func (s *Service) protectedIDs(ctx context.Context) (map[domain.SessionID]bool, error) {
	out := map[domain.SessionID]bool{}
	if s.protector == nil {
		return out, nil
	}
	sessions, artifacts, texts, err := s.protector.LiveBacklogArtifactRefs(ctx)
	if err != nil {
		return nil, fmt.Errorf("read backlog references: %w", err)
	}
	for _, id := range sessions {
		out[domain.SessionID(id)] = true
	}
	for _, a := range artifacts {
		out[domain.SessionID(a)] = true // a bare session id ref
	}
	for _, t := range append(artifacts, texts...) {
		for _, id := range pathTokens(t) {
			out[domain.SessionID(id)] = true
		}
	}
	return out, nil
}

// pathTokens returns every id-like word (letters, digits, '-', '_', '.') in text
// that is directly followed by '/', i.e. the first segment of a path. A word
// glued to a longer id never matches: "agent-orchestrator-4/" does not name
// "agent-orchestrator-494".
func pathTokens(text string) []string {
	var out []string
	start := -1
	for i := 0; i <= len(text); i++ {
		var c byte
		if i < len(text) {
			c = text[i]
		}
		word := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.'
		switch {
		case word && start < 0:
			start = i
		case !word && start >= 0:
			if c == '/' {
				out = append(out, text[start:i])
			}
			start = -1
		}
	}
	return out
}

func (s *Service) dir(rec domain.SessionRecord) string {
	return cmp.Or(rec.Metadata.ArtifactDir, sessionartifacts.Dir(s.dataDir, rec.ID))
}

// managed reports whether the session's artifacts live in the canonical
// directory. The Deleter only ever removes that directory, so a session with a
// different ArtifactDir is measured but never archived or deleted: archiving
// one directory and deleting another would report bytes freed that were not.
func (s *Service) managed(rec domain.SessionRecord) bool {
	custom := rec.Metadata.ArtifactDir
	return custom == "" || filepath.Clean(custom) == filepath.Clean(sessionartifacts.Dir(s.dataDir, rec.ID))
}

func (s *Service) eligible(rec domain.SessionRecord, cutoff time.Time) bool {
	return rec.IsTerminated && rec.UpdatedAt.Before(cutoff) && s.managed(rec)
}

func validWindow(days int) bool { return days >= 1 && days <= MaxOlderThanDays }

// Report measures every session's artifact directory. Sessions without any
// artifact bytes are left out.
func (s *Service) Report(ctx context.Context, olderThanDays int) (Report, error) {
	if olderThanDays == 0 {
		olderThanDays = DefaultOlderThanDays
	}
	if !validWindow(olderThanDays) {
		return Report{}, fmt.Errorf("%w: olderThanDays must be 1-%d", ErrInvalid, MaxOlderThanDays)
	}
	recs, err := s.sessions.ListAllSessions(ctx)
	if err != nil {
		return Report{}, err
	}
	prot, err := s.protectedIDs(ctx)
	if err != nil {
		return Report{}, err
	}
	cutoff := s.now().AddDate(0, 0, -olderThanDays)
	out := Report{OlderThanDays: olderThanDays, WarnBytes: s.warnBytes, Entries: []Entry{}}
	for _, rec := range recs {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		size, versions, files, err := measure(ctx, s.dir(rec))
		if err != nil {
			return Report{}, err
		}
		if size == 0 {
			continue
		}
		e := Entry{
			SessionID: rec.ID, ProjectID: rec.ProjectID, DisplayName: rec.DisplayName,
			Terminated: rec.IsTerminated, UpdatedAt: rec.UpdatedAt.UTC(),
			SizeBytes: size, VersionsBytes: versions, Files: files,
			OverWarn: size >= s.warnBytes, Protected: prot[rec.ID],
		}
		e.Eligible = s.eligible(rec, cutoff) && !e.Protected
		out.TotalBytes += size
		out.VersionsBytes += versions
		if e.Eligible {
			out.EligibleBytes += size
		}
		out.Entries = append(out.Entries, e)
	}
	sort.SliceStable(out.Entries, func(i, j int) bool { return out.Entries[i].SizeBytes > out.Entries[j].SizeBytes })
	return out, nil
}

// measure sums the regular files under dir. The top-level version history is
// also totalled on its own. A missing directory measures as empty.
func measure(ctx context.Context, dir string) (size, versions int64, files int, err error) {
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == dir {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // an entry that cannot be stat'd is skipped, not fatal
		}
		size += info.Size()
		files++
		if rel, err := filepath.Rel(dir, path); err == nil && firstSegment(rel) == sessionartifacts.VersionsDir {
			versions += info.Size()
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, 0, nil
	}
	return size, versions, files, err
}

func firstSegment(rel string) string {
	rel = filepath.ToSlash(rel)
	for i := range rel {
		if rel[i] == '/' {
			return rel[:i]
		}
	}
	return rel
}

// Retain applies action to each listed session that is terminated and older
// than olderThanDays; any other session is skipped with a reason. A failure on
// one session does not stop the rest. The caller is responsible for having the
// user confirm the action.
func (s *Service) Retain(ctx context.Context, action Action, olderThanDays int, ids []domain.SessionID) ([]Result, error) {
	if !action.Valid() {
		return nil, fmt.Errorf("%w: action must be archive or delete", ErrInvalid)
	}
	if !validWindow(olderThanDays) {
		return nil, fmt.Errorf("%w: olderThanDays must be 1-%d", ErrInvalid, MaxOlderThanDays)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: sessionIds is empty", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs, err := s.sessions.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[domain.SessionID]domain.SessionRecord, len(recs))
	for _, rec := range recs {
		byID[rec.ID] = rec
	}
	prot, err := s.protectedIDs(ctx)
	if err != nil {
		return nil, err
	}
	cutoff := s.now().AddDate(0, 0, -olderThanDays)
	results := make([]Result, 0, len(ids))
	seen := make(map[domain.SessionID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := ctx.Err(); err != nil {
			return results, err
		}
		rec, ok := byID[id]
		switch {
		case !ok:
			results = append(results, Result{SessionID: id, Status: StatusSkipped, Reason: "session not found"})
		case !rec.IsTerminated:
			results = append(results, Result{SessionID: id, Status: StatusSkipped, Reason: "session is not terminated"})
		case prot[id]:
			results = append(results, Result{SessionID: id, Status: StatusSkipped, Reason: ProtectedReason})
		case !s.managed(rec):
			results = append(results, Result{SessionID: id, Status: StatusSkipped, Reason: "artifacts are outside the managed artifact directory"})
		case !s.eligible(rec, cutoff):
			results = append(results, Result{SessionID: id, Status: StatusSkipped, Reason: fmt.Sprintf("session was active within the last %d days", olderThanDays)})
		default:
			results = append(results, s.retainOne(ctx, action, rec))
		}
	}
	return results, nil
}

func (s *Service) retainOne(ctx context.Context, action Action, rec domain.SessionRecord) Result {
	res := Result{SessionID: rec.ID}
	dir := s.dir(rec)
	size, _, _, err := measure(ctx, dir)
	if err != nil {
		res.Status, res.Reason = StatusFailed, err.Error()
		return res
	}
	if size == 0 {
		res.Status, res.Reason = StatusSkipped, "no artifact files"
		return res
	}
	res.Status = StatusDeleted
	if action == ActionArchive {
		path, err := s.archive(ctx, rec.ID, dir)
		if err != nil {
			// Nothing was removed: the archive is complete before the delete starts.
			res.Status, res.Reason = StatusFailed, "archive failed: "+err.Error()
			return res
		}
		res.Status, res.ArchivePath = StatusArchived, path
	}
	if err := s.deleter.DeleteSession(ctx, rec.ID); err != nil {
		res.Status, res.Reason = StatusFailed, "remove failed: "+err.Error()
		return res
	}
	res.FreedBytes = size
	return res
}

// archive writes the deliverable files of dir (not the version history or tool
// directories) to a new .tar.gz under the archive directory and returns its
// path. It writes under a temporary name and renames, so a partial archive is
// never mistaken for a complete one. An artifact directory with no deliverable
// files produces no archive and returns an empty path.
func (s *Service) archive(ctx context.Context, id domain.SessionID, dir string) (string, error) {
	archiveDir := filepath.Join(s.dataDir, ArchiveDir)
	if err := os.MkdirAll(archiveDir, 0o700); err != nil {
		return "", err
	}
	final := filepath.Join(archiveDir, fmt.Sprintf("%s-%d.tar.gz", id, s.now().Unix()))
	tmp := final + ".partial"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	written, err := writeTarGz(ctx, f, dir)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil && written == 0 {
		_ = os.Remove(tmp)
		return "", nil
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return final, nil
}

func writeTarGz(ctx context.Context, w io.Writer, dir string) (int, error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	written := 0
	// Files are opened through a root scoped to dir, so a path swapped for a
	// symlink between the walk and the open cannot read outside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		if firstSegment(rel) == sessionartifacts.VersionsDir || sessionartifacts.IgnoredPath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		src, err := root.Open(rel)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, io.LimitReader(src, info.Size()))
		_ = src.Close()
		if copyErr != nil {
			return copyErr
		}
		written++
		return nil
	})
	if err != nil {
		return written, err
	}
	if err := tw.Close(); err != nil {
		return written, err
	}
	return written, gz.Close()
}
