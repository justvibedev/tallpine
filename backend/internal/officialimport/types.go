// Package officialimport migrates project registrations and non-secret global
// settings from an official Agent Orchestrator install (data dir ~/.ao/data)
// into this fork's registry.
//
// The source is strictly read-only. The official database is never opened in
// place: Snapshot first takes a copy-on-write clone (cp -c) under this
// product's own data dir, and every read happens against that clone. Nothing in
// the official data dir is created, written, locked or migrated. Sessions, chat
// history, reports, artifacts, browser data and every credential are out of
// scope (see Skipped).
package officialimport

import "tallpine.eu/tallpine/backend/internal/domain"

// Action is what a run does (or, in a dry run, would do) for one project.
type Action string

const (
	// ActionCreate registers a project the target does not have yet.
	ActionCreate Action = "create"
	// ActionUpdate rewrites an already-imported project whose source differs.
	ActionUpdate Action = "update"
	// ActionUnchanged leaves a project that already matches the source.
	ActionUnchanged Action = "unchanged"
	// ActionConflict refuses a project that collides with a different target project.
	ActionConflict Action = "conflict"
	// ActionSkip leaves out a project that cannot or should not be imported.
	ActionSkip Action = "skip"
	// ActionError marks a project whose source config could not be mapped or written.
	ActionError Action = "error"
)

// Options configure one import run.
type Options struct {
	// SourceDataDir is the official AO data dir (the directory holding ao.db).
	SourceDataDir string
	// StagingDir is where the snapshot is placed; it must live under this
	// product's own state root.
	StagingDir string
	// DryRun plans everything and writes nothing.
	DryRun bool
	// ProjectIDs limits the run to these source project ids. Empty selects all.
	ProjectIDs []string
	// IncludeSettings also applies the global settings that map 1:1.
	IncludeSettings bool
}

// ProjectPlan is the outcome for one source project.
type ProjectPlan struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Kind        string   `json:"kind"`
	Archived    bool     `json:"archived,omitempty"`
	RepoURL     string   `json:"repoUrl,omitempty"`
	ChildRepos  int      `json:"childRepos,omitempty"`
	Action      Action   `json:"action"`
	Selected    bool     `json:"selected"`
	Applied     bool     `json:"applied,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	TargetID    string   `json:"targetId,omitempty"`
	TargetPath  string   `json:"targetPath,omitempty"`
	ConfigItems []string `json:"configItems,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	Error       string   `json:"error,omitempty"`

	record domain.ProjectRecord
	repos  []domain.WorkspaceRepoRecord
}

// SettingChange is one global setting the import maps across.
type SettingChange struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	Current string `json:"current,omitempty"`
	Action  Action `json:"action"`
	Applied bool   `json:"applied,omitempty"`
	Error   string `json:"error,omitempty"`

	apply func() error
}

// Skipped names something deliberately not imported, with the reason.
type Skipped struct {
	What  string `json:"what"`
	Why   string `json:"why"`
	Count int    `json:"count,omitempty"`
}

// Counts summarizes the selected projects by action.
type Counts struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Unchanged int `json:"unchanged"`
	Conflict  int `json:"conflict"`
	Skip      int `json:"skip"`
	Error     int `json:"error"`
}

// Report is the structured outcome of one run.
type Report struct {
	SourceDataDir   string          `json:"sourceDataDir"`
	Found           bool            `json:"found"`
	DryRun          bool            `json:"dryRun"`
	IncludeSettings bool            `json:"includeSettings"`
	Projects        []ProjectPlan   `json:"projects"`
	Settings        []SettingChange `json:"settings,omitempty"`
	Skipped         []Skipped       `json:"skipped,omitempty"`
	Warnings        []string        `json:"warnings,omitempty"`
	Counts          Counts          `json:"counts"`
}

// HasFailures reports whether any selected project or setting errored.
func (r Report) HasFailures() bool {
	return r.Counts.Error > 0 || r.Counts.Conflict > 0
}
