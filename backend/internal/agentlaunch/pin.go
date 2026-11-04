package agentlaunch

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
)

var pinMu sync.Mutex

// pinDirectory uses the install directory only when it contains no sibling
// executables. Shared installs get a shim so pinning tallpine cannot promote node,
// git, or other tools ahead of the agent's selected runtime.
func pinDirectory(exe, dataDir string) (string, error) {
	dir := pinnedDirForExecutable(exe)
	if dir == "" {
		return "", nil
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return dir, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect Tallpine install directory: %w", err)
	}
	shared := false // sibling executables: the dir must never be pinned as is
	for _, entry := range entries {
		if entry.Name() == filepath.Base(exe) || entry.IsDir() {
			continue
		}
		info, statErr := os.Stat(filepath.Join(dir, entry.Name()))
		if statErr != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if runtime.GOOS == "windows" {
			switch strings.ToLower(filepath.Ext(entry.Name())) {
			case ".exe", ".com", ".cmd", ".bat":
				shared = true
			}
		} else if info.Mode()&0o111 != 0 {
			shared = true
		}
	}
	// A directory lacking the tp alias cannot serve it, so it is shimmed too.
	missingAlias := false
	if _, err := os.Stat(filepath.Join(dir, brand.CLIAlias)); err != nil {
		missingAlias = true
	}
	if !shared && !missingAlias {
		return dir, nil
	}
	if !filepath.IsAbs(dataDir) {
		return "", fmt.Errorf("Tallpine shim data directory must be absolute, got %q", dataDir) //nolint:staticcheck // ST1005: error text starts with the product name
	}
	shimDir, err := createShimDir(exe, dataDir)
	if err != nil {
		if shared {
			// Pinning a dir with other executables would promote its node/git
			// ahead of the agent runtime; stay unpinned instead.
			return "", err
		}
		// The dir holds only the daemon, so a shim directory we cannot write
		// must not leave the session unpinned: pin the daemon's own directory.
		return dir, nil //nolint:nilerr // intentional degrade to the daemon dir
	}
	return shimDir, nil
}

func createShimDir(exe, dataDir string) (string, error) {
	absolute, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(absolute))
	shimDir := filepath.Join(dataDir, "runtime", brand.CLIName+"-path", fmt.Sprintf("%x", sum[:16]))
	pinMu.Lock()
	defer pinMu.Unlock()
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return "", err
	}
	quoted := strings.ReplaceAll(absolute, "'", `'"'"'`)
	winPath := strings.ReplaceAll(absolute, "%", "%%")
	// tallpine is the canonical name and tp its alias.
	for _, name := range []string{brand.CLIName, brand.CLIAlias} {
		script := "#!/bin/sh\nexec '" + quoted + "' \"$@\"\n"
		if runtime.GOOS == "windows" {
			name += ".cmd"
			script = "@echo off\r\n\"" + winPath + "\" %*\r\n"
		}
		if err := writeShim(shimDir, name, script); err != nil {
			return "", err
		}
	}
	if runtime.GOOS == "windows" {
		if err := ensureWindowsTallpineExecutable(shimDir, absolute); err != nil {
			return "", err
		}
	}
	return shimDir, nil
}

func writeShim(shimDir, name, script string) error {
	target := filepath.Join(shimDir, name)
	if content, err := os.ReadFile(target); err == nil && string(content) == script {
		return nil
	}
	temp, err := os.CreateTemp(shimDir, "."+brand.CLIName+"-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if _, err := temp.WriteString(script); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, 0o700); err != nil { //nolint:gosec // executable Tallpine shim
		return err
	}
	return os.Rename(tempPath, target)
}

func ensureWindowsTallpineExecutable(shimDir, executable string) error {
	return ensureWindowsTallpineExecutableWithLink(shimDir, executable, os.Link)
}

func ensureWindowsTallpineExecutableWithLink(shimDir, executable string, link func(string, string) error) error {
	target := filepath.Join(shimDir, brand.CLIName+".exe")
	sourceInfo, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("stat Tallpine executable: %w", err)
	}
	if targetInfo, targetErr := os.Stat(target); targetErr == nil && os.SameFile(sourceInfo, targetInfo) {
		return nil
	} else if targetErr == nil && targetInfo.Size() == sourceInfo.Size() && targetInfo.ModTime().Equal(sourceInfo.ModTime()) {
		return nil
	}
	temp, err := os.CreateTemp(shimDir, "."+brand.CLIName+"-exe-*")
	if err != nil {
		return fmt.Errorf("create Tallpine executable shim: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		return err
	}
	defer func() { _ = os.Remove(tempPath) }()
	if err := os.Remove(tempPath); err != nil {
		return err
	}
	if err := link(executable, tempPath); err != nil {
		if err := copyExecutable(executable, tempPath, sourceInfo.ModTime()); err != nil {
			return fmt.Errorf("copy Tallpine executable after link failed: %w", err)
		}
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace Tallpine executable shim: %w", err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		return fmt.Errorf("install Tallpine executable shim: %w", err)
	}
	return nil
}

func copyExecutable(source, target string, modTime time.Time) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700) //nolint:gosec // executable Tallpine shim
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(target, modTime, modTime)
}
