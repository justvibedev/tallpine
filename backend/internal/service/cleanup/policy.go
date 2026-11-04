package cleanup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrInvalidPolicy marks a policy change the service refuses.
var ErrInvalidPolicy = errors.New("invalid cleanup policy")

// Bounds of a retention window, in days.
const (
	MaxRetentionDays = 3650
)

// DeliverableAction is what happens to a long-terminated session's deliverable
// files once the user confirmed it (or opted into AutoDeliverables).
type DeliverableAction string

// Deliverable actions.
const (
	DeliverableArchive DeliverableAction = "archive"
	DeliverableDelete  DeliverableAction = "delete"
)

// Policy is the user's cleanup configuration. It is stored as one JSON
// document; a key the document lacks keeps its default, so a rule added later
// needs no migration.
type Policy struct {
	// Automatic runs the scheduled passes. A manual run works either way.
	Automatic     bool                `json:"automatic"`
	Worktrees     WorktreePolicy      `json:"worktrees"`
	Artifacts     ArtifactsPolicy     `json:"artifacts"`
	SessionTemp   SessionTempPolicy   `json:"sessionTemp"`
	HarnessCaches HarnessCachesPolicy `json:"harnessCaches"`
	SystemTemp    SystemTempPolicy    `json:"systemTemp"`
	GoBuildCache  GoBuildCachePolicy  `json:"goBuildCache"`
	Containers    ContainersPolicy    `json:"containers"`
	Branches      BranchesPolicy      `json:"branches"`
	Backlog       BacklogPolicy       `json:"backlog"`
}

// BacklogPolicy governs the backlog inbox: an agent-filed item nobody has
// touched for InboxExpiryDays is dropped (recoverable: it stays in the
// archive). Human-filed items never expire.
type BacklogPolicy struct {
	Enabled         bool `json:"enabled"`
	InboxExpiryDays int  `json:"inboxExpiryDays"`
}

// BranchesPolicy governs the tp/ git branches of terminated worker sessions.
// A branch whose commits exist nowhere else is never deleted. While off, runs
// only report what they would delete.
type BranchesPolicy struct {
	Enabled bool `json:"enabled"`
	// MergedRetentionDays deletes a local branch this long after its session
	// ended when its work is merged: its tip is in another branch on the
	// server (confirmed with it), or it is the head of the session's merged
	// PR.
	MergedRetentionDays int `json:"mergedRetentionDays"`
	// StaleRetentionDays deletes an unmerged local branch this long after its
	// session ended when every commit is also on a remote branch.
	StaleRetentionDays int `json:"staleRetentionDays"`
	// DeleteRemote also deletes the remote branch of a merged PR (only while
	// it still points at the PR's head). Off by default.
	DeleteRemote bool `json:"deleteRemote"`
}

// ContainersPolicy governs Docker containers and volumes labeled
// tallpine.session=<id> whose session has ended. tallpine.spare=true opts one out, as does
// the project's containerReap setting.
type ContainersPolicy struct {
	Enabled bool `json:"enabled"`
	// VolumeRetentionDays keeps a labeled volume this long after its session
	// ended, in case its data is wanted; containers go on the next pass.
	VolumeRetentionDays int `json:"volumeRetentionDays"`
}

// SessionTempPolicy governs session temp directories (<data dir>/tmp) that
// outlived their session: a termination the daemon did not see, or a removal
// it did not finish.
type SessionTempPolicy struct {
	Enabled bool `json:"enabled"`
}

// HarnessCachesPolicy governs the per-project caches Tallpine points agents at
// (<data dir>/caches; today the Gradle user home).
type HarnessCachesPolicy struct {
	Enabled bool `json:"enabled"`
	// RetentionDays removes a project's cache once the project has had no live
	// session for this long. A project with a live session keeps its cache.
	RetentionDays int `json:"retentionDays"`
}

// SystemTempPolicy governs leftovers in the system temp directory, which Tallpine
// cannot attribute to a session. Off by default; while off, a run only
// reports what it would remove.
type SystemTempPolicy struct {
	Enabled bool `json:"enabled"`
	// MinAgeDays only considers entries not modified for this long.
	MinAgeDays int `json:"minAgeDays"`
	// Patterns are shell globs matched against top-level entry names.
	Patterns []string `json:"patterns"`
}

// GoBuildCachePolicy governs the user-wide Go build cache (GOCACHE), which lives
// outside the data dir and is shared with the user's own Go work. Off by
// default; while off, a run only reports what it would remove.
type GoBuildCachePolicy struct {
	Enabled bool `json:"enabled"`
}

// WorktreePolicy governs the worktrees of terminated sessions. A worktree that
// holds anything its removal would lose (see GitInspect), or one a live
// session uses, is never removed.
type WorktreePolicy struct {
	Enabled bool `json:"enabled"`
	// RetentionDays keeps a terminated session's worktree this long, so a
	// restore soon after still finds it. Zero removes it on the next pass.
	RetentionDays int `json:"retentionDays"`
	// RequireMerged keeps a worktree whose commits are only on its own
	// branch: only work that is merged or pushed elsewhere lets it go.
	RequireMerged bool `json:"requireMerged"`
}

// ArtifactsPolicy governs the artifact directories of terminated sessions.
type ArtifactsPolicy struct {
	Enabled bool `json:"enabled"`
	// RetentionDays is how long after a session ended its artifacts are left
	// untouched.
	RetentionDays int `json:"retentionDays"`
	// AutoPrune removes the version history and tool directories
	// (node_modules, virtual environments, __pycache__) of sessions past
	// retention without asking. Off by default: like deliverables, they are
	// only removed once the user confirms (`tallpine cleanup --confirm-artifacts`).
	AutoPrune bool `json:"autoPrune"`
	// DeliverableAction applies to the deliverable files of sessions past
	// retention once confirmed.
	DeliverableAction DeliverableAction `json:"deliverableAction"`
	// AutoDeliverables applies DeliverableAction without asking. Off by
	// default: deliverables are only archived or deleted after the user
	// confirms.
	AutoDeliverables bool `json:"autoDeliverables"`
}

// DefaultPolicy is the policy of an install that never changed it. Scheduled
// passes only remove what can be regenerated or is already safe elsewhere:
// worktrees of sessions that ended a week ago whose work is merged or pushed.
// Anything under artifacts waits for the user's confirmation. The first
// scheduled pass is a dry run (see Service.Tick).
func DefaultPolicy() Policy {
	return Policy{
		Automatic: true,
		Worktrees: WorktreePolicy{Enabled: true, RetentionDays: 7, RequireMerged: true},
		Artifacts: ArtifactsPolicy{
			Enabled: true, RetentionDays: 30, AutoPrune: false,
			DeliverableAction: DeliverableArchive,
		},
		SessionTemp:   SessionTempPolicy{Enabled: true},
		HarnessCaches: HarnessCachesPolicy{Enabled: true, RetentionDays: 14},
		SystemTemp: SystemTempPolicy{
			MinAgeDays: 3,
			// Only names VO itself would create: anything broader (tmp.* is
			// what `mktemp -d` picks for every program; tallpine-* is the official
			// Tallpine's) would remove other programs' files. Users
			// add the patterns of their own leftovers, e.g. release-check.*.
			Patterns: []string{"vo-*"},
		},
		// Off until the user has seen what it would remove: the session label
		// is not scoped to one install.
		GoBuildCache: GoBuildCachePolicy{Enabled: false},
		Containers:   ContainersPolicy{Enabled: false, VolumeRetentionDays: 7},
		// Off until the user has seen what it would delete: installs that
		// already had their first dry run would otherwise delete on the next
		// pass.
		Branches: BranchesPolicy{Enabled: false, MergedRetentionDays: 7, StaleRetentionDays: 30},
		// On by default: an expired item is only marked dropped and stays recoverable.
		Backlog: BacklogPolicy{Enabled: true, InboxExpiryDays: 14},
	}
}

// maxSystemTempPatterns bounds the pattern list.
const maxSystemTempPatterns = 50

// Validate reports the first out-of-range value.
func (p Policy) Validate() error {
	if p.Worktrees.RetentionDays < 0 || p.Worktrees.RetentionDays > MaxRetentionDays {
		return fmt.Errorf("%w: worktrees.retentionDays must be 0-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if p.Artifacts.RetentionDays < 1 || p.Artifacts.RetentionDays > MaxRetentionDays {
		return fmt.Errorf("%w: artifacts.retentionDays must be 1-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if p.Artifacts.DeliverableAction != DeliverableArchive && p.Artifacts.DeliverableAction != DeliverableDelete {
		return fmt.Errorf("%w: artifacts.deliverableAction must be archive or delete", ErrInvalidPolicy)
	}
	if p.Branches.MergedRetentionDays < 0 || p.Branches.MergedRetentionDays > MaxRetentionDays {
		return fmt.Errorf("%w: branches.mergedRetentionDays must be 0-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if p.Branches.StaleRetentionDays < 1 || p.Branches.StaleRetentionDays > MaxRetentionDays {
		return fmt.Errorf("%w: branches.staleRetentionDays must be 1-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if p.Backlog.InboxExpiryDays < 1 || p.Backlog.InboxExpiryDays > MaxRetentionDays {
		return fmt.Errorf("%w: backlog.inboxExpiryDays must be 1-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if p.Containers.VolumeRetentionDays < 0 || p.Containers.VolumeRetentionDays > MaxRetentionDays {
		return fmt.Errorf("%w: containers.volumeRetentionDays must be 0-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if p.HarnessCaches.RetentionDays < 1 || p.HarnessCaches.RetentionDays > MaxRetentionDays {
		return fmt.Errorf("%w: harnessCaches.retentionDays must be 1-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if p.SystemTemp.MinAgeDays < 1 || p.SystemTemp.MinAgeDays > MaxRetentionDays {
		return fmt.Errorf("%w: systemTemp.minAgeDays must be 1-%d", ErrInvalidPolicy, MaxRetentionDays)
	}
	if len(p.SystemTemp.Patterns) > maxSystemTempPatterns {
		return fmt.Errorf("%w: systemTemp.patterns holds at most %d patterns", ErrInvalidPolicy, maxSystemTempPatterns)
	}
	for _, pattern := range p.SystemTemp.Patterns {
		if err := validTempPattern(pattern); err != nil {
			return fmt.Errorf("%w: systemTemp.patterns: %w", ErrInvalidPolicy, err)
		}
	}
	return nil
}

// validTempPattern accepts a glob that names entries, not paths, and cannot
// match everything: it needs at least two literal characters.
func validTempPattern(pattern string) error {
	if strings.ContainsAny(pattern, `/\`) {
		return fmt.Errorf("%q: a pattern matches entry names, not paths", pattern)
	}
	if _, err := filepath.Match(pattern, ""); err != nil {
		return fmt.Errorf("%q: %w", pattern, err)
	}
	literal := 0
	for _, r := range pattern {
		if !strings.ContainsRune("*?[]^-!", r) {
			literal++
		}
	}
	if literal < 2 {
		return fmt.Errorf("%q matches too broadly", pattern)
	}
	return nil
}

// FailSafePolicy is what an unreadable policy reads as: the defaults with
// Automatic off. The user may have turned scheduled cleanup or a rule off, and
// a storage failure must never turn deletion back on behind their back; a
// manual run still works.
func FailSafePolicy() Policy {
	p := DefaultPolicy()
	p.Automatic = false
	return p
}

// DecodePolicy reads a stored document over the defaults. A document that
// does not parse, or holds an out-of-range value, yields FailSafePolicy: a
// corrupt row must not turn into a more aggressive policy than the user chose.
func DecodePolicy(doc string) Policy {
	p, _ := decodePolicy(doc)
	return p
}

func decodePolicy(doc string) (Policy, error) {
	p := DefaultPolicy()
	if doc == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return FailSafePolicy(), fmt.Errorf("%w: %w", ErrInvalidPolicy, err)
	}
	if err := p.Validate(); err != nil {
		return FailSafePolicy(), err
	}
	return p, nil
}

// ApplyPatch merges a partial JSON document (only the keys to change) over p.
// Unknown keys and out-of-range values are refused.
func ApplyPatch(p Policy, patch []byte) (Policy, error) {
	if len(bytes.TrimSpace(patch)) == 0 {
		return p, fmt.Errorf("%w: empty change", ErrInvalidPolicy)
	}
	dec := json.NewDecoder(bytes.NewReader(patch))
	dec.DisallowUnknownFields()
	next := p
	if err := dec.Decode(&next); err != nil {
		return p, fmt.Errorf("%w: %w", ErrInvalidPolicy, err)
	}
	if err := next.Validate(); err != nil {
		return p, err
	}
	return next, nil
}

// forStorageCleanup turns on the space-reclaiming rules and leaves every
// window and safety option as the user set it.
func (p Policy) forStorageCleanup() Policy {
	p.Worktrees.Enabled = true
	p.SessionTemp.Enabled = true
	p.HarnessCaches.Enabled = true
	p.GoBuildCache.Enabled = true
	return p
}
