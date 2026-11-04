package storageusage

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"time"
)

// Measure is the size of one tree.
type Measure struct {
	Bytes, Files int64
	Newest       time.Time
	Partial      bool
}

// measureTree walks root without following symlinks and sums the space its
// files occupy, counting a hard-linked file once. It stops at ctx and then
// reports what it has as Partial. A missing root measures zero.
func measureTree(ctx context.Context, root string) Measure {
	var m Measure
	seen := map[inodeKey]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if d != nil && d.IsDir() && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished mid-walk.
		}
		if info.ModTime().After(m.Newest) && !d.IsDir() {
			m.Newest = info.ModTime()
		}
		if d.IsDir() {
			if m.Newest.IsZero() {
				m.Newest = info.ModTime()
			}
			m.Bytes, _, _ = addDir(m.Bytes, info)
			return nil
		}
		n, key, linked := diskBytes(info)
		if linked {
			if seen[key] {
				return nil
			}
			seen[key] = true
		}
		m.Bytes += n
		m.Files++
		return nil
	})
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
		m.Partial = true
	}
	return m
}

// addDir adds a directory's own blocks, which du counts too.
func addDir(total int64, info fs.FileInfo) (int64, inodeKey, bool) {
	n, key, linked := diskBytes(info)
	return total + n, key, linked
}
