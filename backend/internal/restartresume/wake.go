package restartresume

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	wakeFileName   = "restart-wake.json"
	parkedFileName = "restart-parked.json"
)

// WakeMessage tells a worker the harness jobs it started are gone.
func WakeMessage(at time.Time) string {
	return fmt.Sprintf("App restarted at %s; background jobs and monitors you started were stopped. Re-check state (git status, your task, any open PR or running checks) and continue.", at.UTC().Format(time.RFC3339))
}

// WakeID is the stable report id for one worker's restart notice.
func WakeID(id domain.SessionID, at time.Time) string {
	return "rpt_restart_wake_" + string(id) + "_" + at.UTC().Format("20060102T150405")
}

// RecordParked durably notes the chat workers a deliberate quit is about to
// park (ParkAll marks each asleep and ends its process tree, monitors and
// background jobs included), so the next boot can tell them from workers that
// idle hibernation put to sleep earlier. Call it next to Record, before agents
// are parked.
func (t *Tracker) RecordParked(ctx context.Context) error {
	recs, err := t.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Replace, never merge: the set describes this quit only.
	parked := map[domain.SessionID]bool{}
	for _, rec := range recs {
		if wakeCandidate(rec) && rec.HibernatedAt == nil {
			parked[rec.ID] = true
		}
	}
	t.saveParked(parked)
	return nil
}

// wakeCandidate is a live chat-mode worker. A TUI worker's agent runs in a
// terminal host that outlives the daemon and a quit, so its jobs still run.
func wakeCandidate(rec domain.SessionRecord) bool {
	return rec.Kind == domain.KindWorker && !rec.IsTerminated && !rec.IsTaskPreparation &&
		domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
}

// WakeIdle sends every idle chat worker whose provider died one restart notice
// and files an orchestrator-visible report.
//
// Skipped: workers mid-turn or exited, blocked workers with no lost jobs; TUI workers; workers whose
// provider host survived the restart (skip reports true); workers Resume just
// messaged (Send has no activity ack, so they can still read idle); workers
// hibernated before the last quit (nothing of theirs was running; a message
// would revive them); and workers whose last notice is still unanswered (no
// activity since, so a crash loop does not spam them). A worker that ran since
// its last notice gets a fresh one. A failed send is not recorded, so a later
// start retries it.
func (t *Tracker) WakeIdle(ctx context.Context, sender Sender, skip func(domain.SessionID) bool) int {
	recs, err := t.store.ListAllSessions(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "restart wake: list sessions failed", "err", err)
		return 0
	}
	now := t.clock().UTC()
	t.mu.Lock()
	sent := t.pruneWake(recs)
	t.pruneLostJobs(recs)
	parked := t.loadParked()
	resumed := map[domain.SessionID]bool{}
	for id := range t.resumed {
		resumed[id] = true
	}
	t.mu.Unlock()
	woken := 0
	retry := map[domain.SessionID]bool{} // parked workers whose send failed
	// The parked set serves one pass: whatever happens to a worker now, a later
	// start must not mistake it for a quit-parked one.
	defer func() {
		t.mu.Lock()
		t.saveParked(retry)
		t.mu.Unlock()
	}()
	for _, rec := range recs {
		// A blocked worker with lost jobs is still told: the quit ended the dialog
		// it was blocked on along with the jobs.
		jobs := t.lostJobsFor(rec.ID)
		if !wakeCandidate(rec) || rec.Activity.State == domain.ActivityExited ||
			rec.Activity.State == domain.ActivityActive || rec.Activity.State == domain.ActivityBlocked && len(jobs) == 0 {
			continue
		}
		if rec.HibernatedAt != nil && !parked[rec.ID] {
			continue
		}
		if resumed[rec.ID] || skip != nil && skip(rec.ID) {
			continue
		}
		if at, ok := sent[rec.ID]; ok && !rec.Activity.LastActivityAt.After(at) {
			continue
		}
		if err := sender.Send(ctx, rec.ID, WakeMessage(now)+lostJobsSentence(jobs), nil); err != nil {
			slog.Default().WarnContext(ctx, "restart wake: send failed", "session", rec.ID, "err", err)
			if parked[rec.ID] {
				retry[rec.ID] = true
			}
			continue
		}
		woken++
		slog.Default().InfoContext(ctx, "restart wake: notice sent", "session", rec.ID, "lost_jobs", len(jobs))
		t.forgetLostJobs(rec.ID)
		t.mu.Lock()
		sent = t.loadWake()
		sent[rec.ID] = t.clock().UTC()
		t.saveWake(sent)
		t.mu.Unlock()
		if rec.Kind == domain.KindWorker {
			t.noteWoken(rec.ID, rec.ProjectID, now)
		}
	}
	slog.Default().InfoContext(ctx, "restart wake: pass done", "woken", woken, "retry_parked", len(retry))
	return woken
}

// pruneWake drops wake records for sessions that no
// longer exist or are terminated, and persists the result. Caller holds t.mu.
func (t *Tracker) pruneWake(recs []domain.SessionRecord) map[domain.SessionID]time.Time {
	sent := t.loadWake()
	live := map[domain.SessionID]bool{}
	for _, rec := range recs {
		if !rec.IsTerminated {
			live[rec.ID] = true
		}
	}
	removed := 0
	for id := range sent {
		if !live[id] {
			delete(sent, id)
			removed++
		}
	}
	if removed > 0 {
		slog.Default().Info("restart wake: pruned stale records", "removed", removed, "kept", len(sent))
		t.saveWake(sent)
	}
	return sent
}

func (t *Tracker) loadWake() map[domain.SessionID]time.Time {
	out := map[domain.SessionID]time.Time{}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(t.path), wakeFileName))
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (t *Tracker) saveWake(m map[domain.SessionID]time.Time) {
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	p := filepath.Join(filepath.Dir(t.path), wakeFileName)
	if os.WriteFile(p+".tmp", raw, 0o600) == nil {
		_ = os.Rename(p+".tmp", p)
	}
}

func (t *Tracker) loadParked() map[domain.SessionID]bool {
	out := map[domain.SessionID]bool{}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(t.path), parkedFileName))
	if err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (t *Tracker) saveParked(m map[domain.SessionID]bool) {
	p := filepath.Join(filepath.Dir(t.path), parkedFileName)
	if len(m) == 0 {
		_ = os.Remove(p)
		return
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return
	}
	if os.WriteFile(p+".tmp", raw, 0o600) == nil {
		_ = os.Rename(p+".tmp", p)
	}
}
