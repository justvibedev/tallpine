package config

import "path/filepath"

// HardenRoot is the directory fsperm hardens as the Tallpine state root. An
// explicit TALLPINE_DATA_DIR makes StateDir the data dir, and the packaged app
// always sets it, so the real root is recognised from the canonical layout:
// the run file beside a data dir named "data". Any other layout keeps StateDir,
// so a run file parked somewhere shared never has its directory locked down.
func (c Config) HardenRoot() string {
	root := filepath.Dir(c.DataDir)
	if c.RunFilePath != "" && filepath.Base(c.DataDir) == "data" && filepath.Dir(c.RunFilePath) == root {
		return root
	}
	return c.StateDir
}
