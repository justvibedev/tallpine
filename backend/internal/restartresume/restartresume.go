// Package restartresume wakes agents whose turn a daemon restart cut short.
//
// Sessions survive a daemon restart, but a turn in flight does not: the agent
// comes back idle and nothing tells it, or the orchestrator, that its work
// stopped half way. The set of in-flight workers is recorded durably (at a
// deliberate quit before agents are parked, or noted at any other shutdown and
// confirmed at boot, once the chat host is known to be gone), and once startup reconciliation has finished each one gets a
// single resume message plus an orchestrator-visible report.
package restartresume

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

const (
	fileName = "restart-resume.json"
	// maxAge drops an entry nobody could deliver in this long: the work it
	// would resume is stale by then.
	maxAge = 6 * time.Hour
	// defaultAttempts at defaultInterval covers the startup chat resume's retry
	// backoff (about 110s). A slower resume can still be reported as not
	// resumed, which errs toward telling the orchestrator.
	defaultAttempts = 12
	defaultInterval = 15 * time.Second
)

// Entry is one worker whose turn was in flight when the daemon went down.
type Entry struct {
	SessionID     domain.SessionID `json:"sessionId"`
	InterruptedAt time.Time        `json:"interruptedAt"`
}

// Store is the session read surface the tracker needs.
type Store interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// TurnLog is the optional read of when a session last finished a turn. The
// store provides it; a store without it falls back to the activity checks.
type TurnLog interface {
	LastCompletedTurnAt(ctx context.Context, id domain.SessionID) (time.Time, bool, error)
}

// Sender delivers a message to a session's agent (chat or terminal).
type Sender interface {
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
}

// Reports files the orchestrator-facing notice on the worker's behalf.
type Reports interface {
	Create(ctx context.Context, input reportsvc.CreateInput) (domain.ReportRecord, error)
}

// Tracker persists the in-flight set and resumes it.
type Tracker struct {
	path    string
	store   Store
	jobs    JobLister
	clock   func() time.Time
	mu      sync.Mutex
	restore JobRestorer
	resumed map[domain.SessionID]bool // messaged by Resume this run
	notice  map[domain.ProjectID]*projectNotice
	// notResumed holds workers Resume gave up on, reported apart from notice.
	notResumed map[domain.ProjectID][]Entry
	attempts   int
	interval   time.Duration
}

// New builds a tracker keeping its file under dataDir.
func New(dataDir string, store Store) *Tracker {
	return &Tracker{path: filepath.Join(dataDir, fileName), store: store, clock: time.Now, attempts: defaultAttempts, interval: defaultInterval}
}

// RecordIfQuit records the in-flight workers for a deliberate quit, which parks
// agents and so cuts their turns short. Any other shutdown only notes who was
// mid-turn (at the shutdown time); RecordUnclean at the next boot resumes just
// those whose chat host did not survive, since a plain restart detaches and
// leaves chat hosts running their turns.
func (t *Tracker) RecordIfQuit(ctx context.Context, deliberateQuit bool) error {
	if err := t.recordJobs(ctx); err != nil {
		slog.Default().WarnContext(ctx, "restart resume: could not save running background jobs", "err", err)
	}
	if !deliberateQuit {
		// A bare /shutdown (the installer's) says nothing about whether the app
		// stops the agents afterwards. Note who was mid-turn; the next boot
		// resumes only those whose host did not survive (RecordUnclean).
		return t.recordMaybe(ctx)
	}
	if err := t.RecordParked(ctx); err != nil {
		return err
	}
	return t.Record(ctx)
}

// RecordUnclean covers a shutdown that never reached RecordIfQuit (killed by an
// installer, crashed, forced close): at boot, any chat-mode worker or
// orchestrator still stored as mid-turn whose provider host did not survive
// (survived reports true) had its turn cut off. Call it before reconciliation
// rewrites activity. Sessions that were idle, terminated or whose host lives on
// are never recorded.
func (t *Tracker) RecordUnclean(ctx context.Context, survived func(domain.SessionID) bool) error {
	recs, err := t.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropSurvivedJobs(survived)
	entries := t.load()
	known := map[domain.SessionID]bool{}
	for _, e := range entries {
		known[e.SessionID] = true
	}
	maybe := t.loadMaybe()
	t.saveMaybe(nil)
	now := t.clock().UTC()
	changed := false
	var recorded []domain.SessionID
	for _, rec := range recs {
		if !resumable(rec.Kind) || rec.IsTerminated || rec.Activity.State != domain.ActivityActive && maybe[rec.ID].IsZero() ||
			domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat || survived != nil && survived(rec.ID) {
			continue
		}
		// The shutdown time when a bare shutdown noted it; for a crash, the
		// last sign of life. Boot time would dodge maxAge and misreport the restart.
		at := maybe[rec.ID]
		if at.IsZero() {
			at = rec.Activity.LastActivityAt.UTC()
		}
		if at.IsZero() {
			at = now
		}
		if known[rec.ID] {
			if refreshEntry(entries, rec.ID, at, at) {
				recorded = append(recorded, rec.ID)
				changed = true
			}
			continue
		}
		entries = append(entries, Entry{SessionID: rec.ID, InterruptedAt: at})
		recorded = append(recorded, rec.ID)
		changed = true
	}
	slog.Default().InfoContext(ctx, "restart resume: boot recorded cut-off turns", "recorded", recorded, "noted_at_shutdown", len(maybe), "already_known", len(known))
	if !changed {
		return nil
	}
	return t.save(entries)
}

// Record merges the workers that are mid-turn right now into the durable set.
// Call it at a deliberate quit, before agents are parked.
func (t *Tracker) Record(ctx context.Context) error {
	recs, err := t.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	entries := t.load()
	known := map[domain.SessionID]bool{}
	for _, e := range entries {
		known[e.SessionID] = true
	}
	now := t.clock().UTC()
	var recorded []domain.SessionID
	for _, rec := range recs {
		if !resumable(rec.Kind) || rec.IsTerminated || rec.Activity.State != domain.ActivityActive {
			continue
		}
		if known[rec.ID] {
			if refreshEntry(entries, rec.ID, rec.Activity.LastActivityAt.UTC(), now) {
				recorded = append(recorded, rec.ID)
			}
			continue
		}
		entries = append(entries, Entry{SessionID: rec.ID, InterruptedAt: now})
		recorded = append(recorded, rec.ID)
	}
	slog.Default().InfoContext(ctx, "restart resume: quit recorded in-flight turns", "recorded", recorded, "pending_total", len(entries))
	return t.save(entries)
}

// refreshEntry moves id's entry to at when evidence shows the agent was working
// after that entry's cut-off. An entry left on file is a worker cut off again
// once it has been restored, and the new cut-off needs its own time and notice
// (B622). Older evidence is a stale state, which leaves the entry alone.
func refreshEntry(entries []Entry, id domain.SessionID, evidence, at time.Time) bool {
	for i := range entries {
		if entries[i].SessionID == id && evidence.After(entries[i].InterruptedAt) {
			entries[i].InterruptedAt = at
			return true
		}
	}
	return false
}

// Resume delivers one resume message to every recorded worker that is still
// alive and idle, notes it for NotifyRestart, and forgets the entry. A
// session that was killed, has exited, or is already working again is dropped
// without a message; a send that fails is retried a few times.
func (t *Tracker) Resume(ctx context.Context, sender Sender) {
	for attempt := 0; attempt < t.attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(t.interval):
			}
		}
		if t.pass(ctx, sender) == 0 {
			return
		}
	}
	if ctx.Err() == nil {
		t.noteNotResumed(ctx)
	}
}

// pass handles every entry once and reports how many remain for a retry.
func (t *Tracker) pass(ctx context.Context, sender Sender) int {
	t.mu.Lock()
	entries := t.load()
	t.mu.Unlock()
	var keep []Entry
	for _, e := range entries {
		if ctx.Err() != nil {
			keep = append(keep, e)
			continue
		}
		if t.clock().Sub(e.InterruptedAt) > maxAge {
			slog.Default().InfoContext(ctx, "restart resume: entry expired", "session", e.SessionID, "interruptedAt", e.InterruptedAt)
			continue
		}
		rec, ok, err := t.store.GetSession(ctx, e.SessionID)
		if err != nil {
			keep = append(keep, e)
			continue
		}
		if ok && resumable(rec.Kind) && !rec.IsTerminated && rec.Activity.State == domain.ActivityExited {
			// Reconcile marks a chat whose host died as exited and restarts it
			// asynchronously; keep the entry (bounded by maxAge) so a slow
			// restart, or a later boot, still delivers the note.
			keep = append(keep, e)
			continue
		}
		if ok && t.completedTurnSince(ctx, e) {
			// A turn finished normally after the interruption, so the agent has
			// resumed and moved on; the note would claim a cut-off that is over.
			slog.Default().InfoContext(ctx, "restart resume: skipped, turn completed since the restart", "session", e.SessionID)
			continue
		}
		if !ok || !resumable(rec.Kind) || rec.IsTerminated || rec.Activity.State == domain.ActivityExited ||
			rec.Activity.State == domain.ActivityActive && rec.Activity.LastActivityAt.After(e.InterruptedAt) {
			slog.Default().InfoContext(ctx, "restart resume: skipped", "session", e.SessionID, "found", ok, "terminated", rec.IsTerminated, "state", rec.Activity.State)
			// Active with activity after the interruption is working again; an
			// Active state older than that is stale and still needs the note.
			continue
		}
		jobs := t.lostJobsFor(e.SessionID)
		if err := sender.Send(ctx, e.SessionID, messageFor(rec.Kind, e.InterruptedAt)+resumeJobsNotice(rec, jobs), nil); err != nil {
			slog.Default().WarnContext(ctx, "restart resume: send failed; will retry", "session", e.SessionID, "err", err)
			keep = append(keep, e)
			continue
		}
		slog.Default().InfoContext(ctx, "restart resume: resume message sent", "session", e.SessionID, "kind", rec.Kind, "lost_jobs", len(jobs))
		t.forgetLostJobs(e.SessionID)
		t.mu.Lock()
		if t.resumed == nil {
			t.resumed = map[domain.SessionID]bool{}
		}
		t.resumed[e.SessionID] = true
		t.mu.Unlock()
		// The report service only takes worker reports; an orchestrator is the
		// report reader, so it gets the resume message alone.
		if rec.Kind == domain.KindWorker {
			t.noteResumed(e, rec.ProjectID)
		}
	}
	// Sends can block for seconds, so the file may have gained entries (a Record
	// at the next shutdown) since it was read: reload and drop only what this
	// pass handled instead of overwriting it.
	retry := map[Entry]bool{}
	for _, e := range keep {
		retry[e] = true
	}
	handled := map[Entry]bool{}
	for _, e := range entries {
		if !retry[e] {
			handled[e] = true
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var remaining []Entry
	for _, e := range t.load() {
		if !handled[e] {
			remaining = append(remaining, e)
		}
	}
	if err := t.save(remaining); err != nil {
		slog.Default().WarnContext(ctx, "restart resume: could not save pending set", "err", err)
	}
	return len(remaining)
}

// completedTurnSince reports whether the session finished a turn after the
// interruption. Unlike activity timestamps, which the restart itself bumps,
// this only moves when a turn ends.
func (t *Tracker) completedTurnSince(ctx context.Context, e Entry) bool {
	log, ok := t.store.(TurnLog)
	if !ok {
		return false
	}
	at, found, err := log.LastCompletedTurnAt(ctx, e.SessionID)
	if err != nil {
		slog.Default().WarnContext(ctx, "restart resume: could not read last completed turn; not skipping", "session", e.SessionID, "err", err)
		return false
	}
	return found && at.After(e.InterruptedAt)
}

// resumable reports whether a session kind is woken after a restart: workers
// and the orchestrator, whose in-flight turn is cut short just the same.
func resumable(k domain.SessionKind) bool {
	return k == domain.KindWorker || k == domain.KindOrchestrator
}

// messageFor words the resume for the session kind: the orchestrator has no
// task or PR of its own to check, it has reports and worker states.
func messageFor(k domain.SessionKind, at time.Time) string {
	if k == domain.KindOrchestrator {
		return fmt.Sprintf("Your previous turn was interrupted by an app restart at %s. The \"interrupted by user\" marker on your last tool call came from the restart, not the user. Verify what that call was doing, check your workers' states and any unread reports, then continue your task.", at.UTC().Format(time.RFC3339))
	}
	return Message(at)
}

// Message is the text the resumed agent receives.
func Message(at time.Time) string {
	return fmt.Sprintf("Your previous turn was interrupted by an app restart at %s. If your last tool call shows \"interrupted by user\", that came from the restart, not the user. Verify what it was doing, check your state (git status, your task and any open PR) and continue your task.", at.UTC().Format(time.RFC3339))
}

// ReportID is the stable report id for one interruption, so a repeat files nothing new.
func ReportID(e Entry) string {
	return "rpt_restart_resume_" + string(e.SessionID) + "_" + e.InterruptedAt.UTC().Format("20060102T150405")
}

func (t *Tracker) load() []Entry {
	raw, err := os.ReadFile(t.path)
	if err != nil {
		return nil
	}
	var entries []Entry
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	return entries
}

func (t *Tracker) save(entries []Entry) error {
	if len(entries) == 0 {
		if err := os.Remove(t.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	tmp := t.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, t.path)
}

const maybeFileName = "restart-maybe.json"

// recordMaybe replaces the set of chat sessions that were mid-turn at a
// shutdown that did not say whether agents would be stopped.
func (t *Tracker) recordMaybe(ctx context.Context) error {
	recs, err := t.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	now := t.clock().UTC()
	set := map[domain.SessionID]time.Time{}
	for _, rec := range recs {
		if resumable(rec.Kind) && !rec.IsTerminated && rec.Activity.State == domain.ActivityActive {
			set[rec.ID] = now
		}
	}
	ids := make([]domain.SessionID, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	slog.Default().InfoContext(ctx, "restart resume: shutdown noted mid-turn sessions", "noted", ids)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.saveMaybe(set)
	return nil
}

// NoteCutOff adds a session whose host was lost mid-turn just before this
// shutdown. It was stored as exited by then, so recordMaybe could not see it;
// the next boot treats it like any other noted turn (B627).
func (t *Tracker) NoteCutOff(id domain.SessionID, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	maybe := t.loadMaybe()
	maybe[id] = at.UTC()
	t.saveMaybe(maybe)
}

func (t *Tracker) loadMaybe() map[domain.SessionID]time.Time {
	out := map[domain.SessionID]time.Time{}
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(t.path), maybeFileName)); err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (t *Tracker) saveMaybe(m map[domain.SessionID]time.Time) {
	p := filepath.Join(filepath.Dir(t.path), maybeFileName)
	if len(m) == 0 {
		_ = os.Remove(p)
		return
	}
	if raw, err := json.Marshal(m); err == nil && os.WriteFile(p+".tmp", raw, 0o600) == nil {
		_ = os.Rename(p+".tmp", p)
	}
}
