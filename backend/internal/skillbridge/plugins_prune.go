package skillbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// pruneMu serialises prunes in this process; Claude Code itself writes the
// file too, which prune guards against with a stat recheck before renaming.
var pruneMu sync.Mutex

// PruneWorktreeInstalls removes the project and local scope entries Claude
// Code recorded for a removed worktree (workspace or any path below it) from
// <claude>/plugins/installed_plugins.json and returns how many went. Only
// workspaces strictly below worktreesRoot (Tallpine's managed worktrees) are
// touched; user-scope entries and entries for other paths are kept.
func PruneWorktreeInstalls(o Options, worktreesRoot, workspace string) (int, error) {
	root, ws := cleanAbs(worktreesRoot), cleanAbs(workspace)
	if root == "" || ws == "" || ws == root || !within(ws, root) {
		return 0, nil
	}
	return pruneInstalls(o, func(p string) bool { return within(p, ws) })
}

// PruneStaleWorktreeInstalls removes project and local entries whose path lies
// strictly below worktreesRoot and no longer exists on disk: the backlog of
// worktrees removed before cleanup pruned them. Entries elsewhere are kept.
func PruneStaleWorktreeInstalls(o Options, worktreesRoot string) (int, error) {
	root := cleanAbs(worktreesRoot)
	if root == "" {
		return 0, nil
	}
	return pruneInstalls(o, func(p string) bool {
		if p == root || !within(p, root) {
			return false
		}
		_, err := os.Lstat(p)
		return errors.Is(err, os.ErrNotExist)
	})
}

// pruneInstalls drops project/local entries whose projectPath matches drop. A
// missing or malformed file is not an error: nothing is pruned. Unknown fields
// survive, plugin ids left without installs are removed, and the file is
// replaced atomically, only if it did not change since it was read.
func pruneInstalls(o Options, drop func(projectPath string) bool) (int, error) {
	dir := o.claudeDir()
	if dir == "" {
		return 0, nil
	}
	pruneMu.Lock()
	defer pruneMu.Unlock()
	path := filepath.Join(dir, "plugins", "installed_plugins.json")
	for range 2 {
		n, again, err := pruneOnce(path, drop)
		if !again {
			return n, err
		}
	}
	return 0, errors.New("installed_plugins.json kept changing; not pruned")
}

func pruneOnce(path string, drop func(string) bool) (pruned int, retry bool, err error) {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxPluginFileBytes {
		return 0, false, nil
	}
	raw, err := os.ReadFile(path) //nolint:gosec // a fixed file under the account's Claude config dir
	if err != nil {
		return 0, false, err
	}
	var top map[string]json.RawMessage
	var plugins map[string][]json.RawMessage
	if json.Unmarshal(raw, &top) != nil || json.Unmarshal(top["plugins"], &plugins) != nil {
		return 0, false, nil
	}
	for id, installs := range plugins {
		kept := installs[:0:0]
		for _, in := range installs {
			var e struct {
				Scope       string `json:"scope"`
				ProjectPath string `json:"projectPath"`
			}
			if json.Unmarshal(in, &e) == nil && (e.Scope == "project" || e.Scope == "local") && e.ProjectPath != "" && drop(filepath.Clean(e.ProjectPath)) {
				pruned++
				continue
			}
			kept = append(kept, in)
		}
		if len(kept) == 0 && len(installs) > 0 {
			delete(plugins, id)
		} else {
			plugins[id] = kept
		}
	}
	if pruned == 0 {
		return 0, false, nil
	}
	if top["plugins"], err = json.Marshal(plugins); err != nil {
		return 0, false, err
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return 0, false, err
	}
	out = append(bytes.TrimSpace(out), '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".installed_plugins-*.tmp")
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err = tmp.Write(out); err == nil {
		err = tmp.Chmod(st.Mode().Perm())
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, false, err
	}
	// Claude Code may have rewritten the file since the read; renaming now
	// would drop its change, so read again instead.
	if now, serr := os.Stat(path); serr != nil || now.Size() != st.Size() || !now.ModTime().Equal(st.ModTime()) {
		return 0, true, nil
	}
	return pruned, false, os.Rename(tmp.Name(), path)
}

func cleanAbs(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !filepath.IsAbs(p) {
		return ""
	}
	return filepath.Clean(p)
}

// within reports whether p is root or lies below it.
func within(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}
