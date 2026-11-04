package migratevo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const planVersion = 1

// Step names, in execution order. Launchd and daemon are hooks (PR 4).
const (
	StepRename    = "rename"
	StepDB        = "db"
	StepWorktrees = "worktrees"
	StepClaude    = "claude"
	StepLaunchd   = "launchd"
	StepCLI       = "cli"
	StepDaemon    = "daemon"
)

// DBUpdate is the number of rows a path column rewrite touches.
type DBUpdate struct {
	Table  string `json:"table"`
	Column string `json:"column"`
	Rows   int64  `json:"rows"`
}

// WorktreePlan is one git worktree to repair, with paths already mapped.
type WorktreePlan struct {
	Repo string `json:"repo"` // main repo, at its post-migration path
	Old  string `json:"old"`
	New  string `json:"new"`
}

// ClaudeCopy is one ~/.claude/projects directory clone for a live session.
type ClaudeCopy struct {
	Session string `json:"session"`
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	Bytes   int64  `json:"bytes"`
	Skip    string `json:"skip,omitempty"` // why this copy will not happen
}

// AccountNote is an agent account whose home dir moves with the state root.
// Its stored credentials may be keyed by the old path, so the user must sign
// in again; the migrator never touches the Keychain.
type AccountNote struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Home     string `json:"home"`
}

// Plan is what the migrator will do; it is also the on-disk resume record.
type Plan struct {
	Version            int             `json:"version"`
	Source             string          `json:"source"`
	Target             string          `json:"target"`
	CopyHarnessHistory bool            `json:"copyHarnessHistory"`
	Steps              map[string]bool `json:"steps"`
	DBUpdates          []DBUpdate      `json:"dbUpdates"`
	Worktrees          []WorktreePlan  `json:"worktrees"`
	ClaudeCopies       []ClaudeCopy    `json:"claudeCopies"`
	Accounts           []AccountNote   `json:"accounts"`
}

// Done reports whether a step is recorded as complete.
func (p *Plan) Done(step string) bool { return p.Steps[step] }

// migrationComplete reports whether the steps that move and rewrite the data
// are done; the later steps (launchd, CLI links, daemon) are best effort.
func (p *Plan) migrationComplete() bool {
	return p.Done(StepRename) && p.Done(StepDB) && p.Done(StepWorktrees)
}

// LoadPlan reads the plan file; (nil, nil) means there is none.
func LoadPlan(path string) (*Plan, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse plan %s: %w", path, err)
	}
	if p.Version != planVersion {
		return nil, fmt.Errorf("plan %s has version %d, want %d", path, p.Version, planVersion)
	}
	if p.Steps == nil {
		p.Steps = map[string]bool{}
	}
	return &p, nil
}

// Save writes the plan atomically (temp file + rename).
func (p *Plan) Save(path string) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
