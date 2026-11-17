// Package gitblob reads file contents at a git revision through the hardened
// default git port, so every caller bounds the read the same way and treats a
// path missing at that revision as absent rather than as a failure.
package gitblob

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// Blob is one revision read by Read.
type Blob struct {
	// Data is nil when the blob is absent or over the read limit.
	Data []byte
	// Size is the blob's full size in bytes, also when it was not read.
	Size int64
	// Exists reports whether spec names an object.
	Exists bool
	// Truncated reports that the blob is over the limit and Data was not read.
	Truncated bool
}

// Read reads the blob named by spec ("<rev>:<path>") in dir. A missing object
// (unknown revision or path) is an absent Blob, not an error. A blob larger
// than limit is reported with Exists and Truncated set and no Data.
func Read(ctx context.Context, dir, spec string, limit int64) (Blob, error) {
	git := ports.DefaultGit()
	sizeOut, err := git.Output(ctx, dir, "cat-file", "-s", spec)
	if err != nil {
		//nolint:nilerr // A path missing from this revision is an absent blob.
		return Blob{}, nil
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if err != nil {
		return Blob{}, fmt.Errorf("parse git blob size: %w", err)
	}
	if size > limit {
		return Blob{Size: size, Exists: true, Truncated: true}, nil
	}
	out, err := git.Output(ctx, dir, "cat-file", "blob", spec)
	if err != nil {
		//nolint:nilerr // A disappearing object is surfaced as an absent blob.
		return Blob{}, nil
	}
	return Blob{Data: []byte(out), Size: size, Exists: true}, nil
}

// ReadAt reads path at ref in dir and returns at most limit bytes of it: a
// larger blob comes back as its first limit bytes, so a caller that caps the
// text itself (read limit+1 to detect the overflow) never loads the whole file.
// found is false when ref or path does not exist; the error is reserved for
// a cancelled context or a blob that vanished mid-read.
func ReadAt(ctx context.Context, dir, ref, path string, limit int64) ([]byte, bool, error) {
	spec := ref + ":" + path
	git := ports.DefaultGit()
	if _, err := git.Output(ctx, dir, "cat-file", "-e", spec); err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, nil
	}
	out, _, err := git.OutputCapped(ctx, dir, int(limit), "cat-file", "blob", spec)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", spec, err)
	}
	return []byte(out), true, nil
}

// Git tree entry modes ListTreeAt reports.
const (
	ModeRegular    = "100644"
	ModeExecutable = "100755"
	ModeSymlink    = "120000"
	ModeSubmodule  = "160000"
)

// Entry is one file of a tree listed by ListTreeAt.
type Entry struct {
	// Path is repository-relative, slash-separated.
	Path string
	// Mode is the git file mode (ModeRegular, ModeExecutable, ModeSymlink, ...).
	Mode string
	// Size is the blob size in bytes; -1 for a submodule.
	Size int64
}

// maxTreeListing bounds the ls-tree output ListTreeAt reads, so a huge tree
// under path cannot become an unbounded buffer.
const maxTreeListing = 1 << 20

// ListTreeAt lists every file under path at ref in dir, recursively, with its
// mode and size. A missing ref or path is an empty listing, not an error; the
// error is reserved for a cancelled context or a listing over the read bound.
func ListTreeAt(ctx context.Context, dir, ref, path string) ([]Entry, error) {
	git := ports.DefaultGit()
	out, cut, err := git.OutputCapped(ctx, dir, maxTreeListing, "ls-tree", "-r", "-l", "-z", "--full-tree", ref, "--", path)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, nil
	}
	if cut {
		return nil, fmt.Errorf("list %s:%s: listing over %d bytes", ref, path, maxTreeListing)
	}
	var entries []Entry
	for _, rec := range strings.Split(out, "\x00") {
		// "<mode> SP <type> SP <object> SP+ <size> TAB <path>"
		meta, p, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 4 {
			continue
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			size = -1
		}
		entries = append(entries, Entry{Path: p, Mode: fields[0], Size: size})
	}
	return entries, nil
}
