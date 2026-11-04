package releaseinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// manifestName is shipped inside the signed app (Contents/Resources), written by
// frontend/scripts/installer-manifest.mjs.
const manifestName = "installer-manifest.json"

type installerManifest struct {
	Dirs  []string          `json:"dirs"`
	Files map[string]string `json:"files"`
}

// newStageDir returns a fresh private (0700) dir under stateDir/release-stage,
// outside the writable build tree, pruning old ones first.
func newStageDir(stateDir string) (string, error) {
	root := filepath.Join(stateDir, "release-stage")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	pruneStaged(root)
	return os.MkdirTemp(root, "install-")
}

// stageInstallerTree reads the manifest from app (the verified private clone),
// then reads each manifest file of build once, checks those bytes against it and
// writes exactly them into dir, so the bytes run are the bytes hashed. The
// manifest's directories must hold no other file. The app signature does not
// cover scripts/ itself; the manifest, being a signed resource, does. It
// returns the staged scripts/release-local.
func stageInstallerTree(app, build, dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(app, "Contents", "Resources", manifestName))
	if err != nil {
		return "", refuse("signed app has no installer manifest: %v", err)
	}
	var m installerManifest
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Files) == 0 {
		return "", refuse("installer manifest unreadable or empty: %v", err)
	}
	for rel, want := range m.Files {
		if err := stageFile(build, dir, rel, want); err != nil {
			return "", err
		}
	}
	if _, ok := m.Files["scripts/release-local"]; !ok {
		return "", refuse("installer manifest does not list scripts/release-local")
	}
	for _, d := range m.Dirs {
		entries, err := os.ReadDir(filepath.Join(build, filepath.FromSlash(d)))
		if err != nil {
			return "", refuse("%s: %v", d, err)
		}
		for _, e := range entries {
			if _, ok := m.Files[d+"/"+e.Name()]; !ok {
				return "", refuse("%s/%s is not in the signed installer manifest", d, e.Name())
			}
		}
	}
	return filepath.Join(dir, "scripts", "release-local"), nil
}

func stageFile(build, dir, rel, want string) error {
	if filepath.IsAbs(rel) || strings.Contains(rel, "..") {
		return refuse("bad manifest path %q", rel)
	}
	resolved, err := within(filepath.Join(build, filepath.FromSlash(rel)), build)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return refuse("%s: %v", rel, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return refuse("%s does not match the signed installer manifest", rel)
	}
	dst := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o700) //nolint:gosec // the private 0700 dir holds a script the daemon execs
}

// pruneStaged removes staged installer copies older than a day (the detached
// installer has long exited by then).
func pruneStaged(root string) {
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}
