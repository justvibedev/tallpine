package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/processalive"
)

// Reconcile terminates compatible authenticated hosts whose durable session no
// longer exists. Live-session hosts, incompatible hosts, and inconclusive probes
// are preserved.
func Reconcile(ctx context.Context, dataDir string, keep map[string]struct{}) error {
	root := HostsRoot(dataDir)
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionID := entry.Name()
		if removeIfEmpty(filepath.Join(root, sessionID)) {
			continue
		}
		if _, ok := keep[sessionID]; ok {
			continue
		}
		d, readErr := readDescriptor(dataDir, sessionID)
		if readErr != nil {
			continue
		}
		if d.Version != ProtocolVersion {
			continue
		}
		if !processalive.Alive(d.PID) {
			// Claim the launch lock before cleaning a dead descriptor. If a new host
			// already owns it, preserve its descriptor instead of deleting ownership
			// state observed by a stale reconciler.
			release, lockErr := acquireHostLock(dataDir, sessionID)
			if lockErr != nil {
				continue
			}
			path, _ := descriptorPath(dataDir, sessionID)
			_ = os.Remove(path)
			release()
			continue
		}
		if shutdownErr := Shutdown(ctx, dataDir, sessionID); shutdownErr != nil {
			errs = append(errs, fmt.Errorf("reap chat host %s: %w", sessionID, shutdownErr))
		}
	}
	return errors.Join(errs...)
}

// removeIfEmpty removes dir when it holds nothing, which is what a finished
// host leaves behind once its descriptor and lock are gone. It reports whether
// dir was removed. os.Remove refuses a directory with entries, so a host that
// appears between the check and the call keeps its files.
func removeIfEmpty(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	_, readErr := f.Readdirnames(1)
	_ = f.Close()
	if !errors.Is(readErr, io.EOF) {
		return false
	}
	return os.Remove(dir) == nil
}
