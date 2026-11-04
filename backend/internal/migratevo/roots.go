package migratevo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

const (
	dataDirName    = "data"
	dbFileName     = "ao.db" // matches storage/sqlite/db.go
	planDirName    = "migration"
	planFileName   = "plan.json"
	legacyPlanFile = "-migration.json" // old ~/.tallpine-migration.json, read once for compatibility
	readmeName     = "README"
	claudeDirName  = ".claude"
)

// Roots holds every filesystem location the migrator reads or writes.
type Roots struct {
	Home           string // $HOME
	Source         string // old state root (~/.vo)
	Target         string // new state root (~/.tallpine)
	ClaudeProjects string // ~/.claude/projects (copy target and source)
	PlanFile       string // ~/.tallpine/migration/plan.json (after the rename)
	CLIDir         string // directory holding the installed CLI; empty disables step 7
	CLIBinary      string // file name of the running CLI binary in CLIDir (vo before the brand flip, tallpine after)
}

// NewRoots derives all locations from home using the brand constants.
func NewRoots(home string) Roots {
	return Roots{
		Home:           home,
		Source:         filepath.Join(home, brand.LegacyHomeDirName),
		Target:         filepath.Join(home, brand.NextHomeDirName),
		ClaudeProjects: filepath.Join(home, claudeDirName, "projects"),
		PlanFile:       filepath.Join(home, brand.NextHomeDirName, planDirName, planFileName),
	}
}

// DefaultRoots resolves roots from the current user's home.
func DefaultRoots() (Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, fmt.Errorf("resolve home dir: %w", err)
	}
	r := NewRoots(home)
	if exe, err := os.Executable(); err == nil {
		r.CLIDir = filepath.Dir(exe)
		r.CLIBinary = filepath.Base(exe)
	}
	return r, nil
}

// SourceDB is the SQLite file before the rename.
func (r Roots) SourceDB() string { return filepath.Join(r.Source, dataDirName, dbFileName) }

// TargetDB is the SQLite file after the rename.
func (r Roots) TargetDB() string { return filepath.Join(r.Target, dataDirName, dbFileName) }

// MapPathBack is the inverse of MapPath: target root back to source root.
func (r Roots) MapPathBack(p string) (string, bool) {
	if rest, ok := strings.CutPrefix(p, r.Target+string(filepath.Separator)); ok {
		return r.Source + string(filepath.Separator) + rest, true
	}
	return p, false
}

// MapPath rewrites p from the source root to the target root. Only a value
// equal to the root or under it (root + "/") is rewritten.
func (r Roots) MapPath(p string) (string, bool) {
	if p == r.Source {
		return r.Target, true
	}
	if rest, ok := strings.CutPrefix(p, r.Source+string(filepath.Separator)); ok {
		return r.Target + string(filepath.Separator) + rest, true
	}
	return p, false
}

// earlyPlanFile is where the plan lives until the whole-tree rename: inside the
// old root, so it moves to PlanFile with the tree and nothing is created in
// $HOME (creating the new root first would also block the rename).
func (r Roots) earlyPlanFile() string {
	return filepath.Join(r.Source, planDirName, planFileName)
}

// legacyHomePlanFile is the pre-fix location, directly in $HOME.
func (r Roots) legacyHomePlanFile() string {
	return filepath.Join(r.Home, brand.NextHomeDirName+legacyPlanFile)
}

// planCandidates lists every place a plan may be, newest layout first.
func (r Roots) planCandidates() []string {
	return []string{r.PlanFile, r.earlyPlanFile(), r.legacyHomePlanFile()}
}

// FindPlanFile returns the path of the recorded plan, or "" when there is none.
func (r Roots) FindPlanFile() string {
	for _, p := range r.planCandidates() {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
	}
	return ""
}

// LoadPlan reads the recorded plan wherever it lives; (nil, nil) means none.
func (r Roots) LoadPlan() (*Plan, error) {
	p := r.FindPlanFile()
	if p == "" {
		return nil, nil
	}
	return LoadPlan(p)
}

// SavePlan writes the plan to its current home: the old root before the rename
// step is recorded, the new root after. Superseded copies are removed.
func (r Roots) SavePlan(p *Plan) error {
	dst := r.earlyPlanFile()
	if p.Done(StepRename) {
		dst = r.PlanFile
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := p.Save(dst); err != nil {
		return err
	}
	for _, old := range r.planCandidates() {
		if old != dst {
			_ = os.Remove(old)
		}
	}
	return nil
}
