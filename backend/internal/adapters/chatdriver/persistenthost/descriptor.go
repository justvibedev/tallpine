package persistenthost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/processalive"
)

// HostsRoot is the directory holding every session's chat host descriptor
// (address and bearer token). Only the daemon and the host processes read it.
func HostsRoot(dataDir string) string {
	return filepath.Join(dataDir, "chat-hosts")
}

func hostDir(dataDir, sessionID string) (string, error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return "", errors.New("invalid chat host session id")
	}
	if !filepath.IsAbs(dataDir) {
		return "", errors.New("chat host data dir must be absolute")
	}
	return filepath.Join(HostsRoot(dataDir), sessionID), nil
}

func descriptorPath(dataDir, sessionID string) (string, error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "host.json"), nil
}

func lockPath(dataDir, sessionID string) (string, error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "host.lock"), nil
}

func acquireHostLock(dataDir, sessionID string) (func(), error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // Directories need execute permission; access is owner-only.
		return nil, err
	}
	path, _ := lockPath(dataDir, sessionID)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // Tallpine-owned capability directory.
	if errors.Is(err, os.ErrExist) {
		owner, readErr := os.ReadFile(path) //nolint:gosec // Tallpine-owned path derived from validated session id.
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(owner)))
		if readErr != nil || parseErr != nil || pid <= 0 || processalive.Alive(pid) {
			return nil, ErrHostExists
		}
		// A host can die after atomically creating its launch lock but before it
		// publishes host.json. Reclaim only a lock whose recorded owner is proven
		// dead, then retry the same O_EXCL acquisition once.
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, ErrHostExists
		}
		f, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // Tallpine-owned capability directory.
		if errors.Is(err, os.ErrExist) {
			return nil, ErrHostExists
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

func readDescriptor(dataDir, sessionID string) (Descriptor, error) {
	path, err := descriptorPath(dataDir, sessionID)
	if err != nil {
		return Descriptor{}, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // Tallpine-owned path derived from validated session id.
	if err != nil {
		return Descriptor{}, err
	}
	var d Descriptor
	if err := json.Unmarshal(b, &d); err != nil {
		return Descriptor{}, fmt.Errorf("decode chat host descriptor: %w", err)
	}
	if d.SessionID != sessionID || d.Address == "" || d.Token == "" {
		return Descriptor{}, errors.New("invalid chat host descriptor")
	}
	return d, nil
}

func writeDescriptor(dataDir string, d Descriptor) error {
	dir, err := hostDir(dataDir, d.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // Directories need execute permission; access is owner-only.
		return err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".host.json.tmp-"+strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	path := filepath.Join(dir, "host.json")
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, 0o600)
}
