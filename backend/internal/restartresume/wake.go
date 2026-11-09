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

// wakeMessageFor words the notice for the session kind: the orchestrator has
// no task or PR of its own to re-check, it has reports and worker states.
func wakeMessageFor(k domain.SessionKind, at time.Time) string {
	if k == domain.KindOrchestrator {
		return fmt.Sprintf("App restarted at %s; background jobs and monitors you started were stopped. Re-check your workers' states and any unread reports, then continue.", at.UTC().Format(time.RFC3339))
	}
	return WakeMessage(at)
}

// WakeID is the stable report id for one worker's restart notice.
func WakeID(id domain.SessionID, at time.Time) string {
	return "rpt_restart_wake_" + string(id) + "_" + at.UTC().Format("20060102T150405")
}

// RecordParked durably notes the chat agents a deliberate quit is about to
// park (ParkAll marks each asleep and ends its process tree, monitors and
// background jobs included), so the next boot can tell them from agents that
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

// wakeCandidate is a live chat-mode worker or orchestrator. A TUI agent runs in
// a terminal host that outlives the daemon and a quit, so its jobs still run.
func wakeCandidate(rec domain.SessionRecord) bool {
	return resumable(rec.Kind) && !rec.IsTerminated && !rec.IsTaskPreparation &&
		domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat
}

// WakeIdle tells every idle chat agent that lost a background job to the
// restart, naming the jobs, so it can rebuild state without waiting for someone
// to open its chat. Sending starts a model turn, and wakes a sleeping session.
// A worker's notice also reaches its orchestrator through the restart report.
//
// Skipped: agents with no recorded lost job, whatever else is true of them;
// agents mid-turn or exited; TUI agents; agents whose provider host survived
// the restart (skip reports true); agents Resume just messaged (Send has no
// activity ack, so they can still read idle); agents hibernated before the last
// quit (nothing of theirs was running; a message would revive them); and agents
// whose last notice is still unanswered (no activity since). A delivered notice
// consumes the job list, so each restart pings at most once; a failed send
// keeps it, so a later start retries.
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
		// A blocked agent with lost jobs is still told: the quit ended the dialog
		// it was blocked on along with the jobs.
		jobs := t.lostJobsFor(rec.ID)
		if !wakeCandidate(rec) || len(jobs) == 0 || rec.Activity.State == domain.ActivityExited ||
			rec.Activity.State == domain.ActivityActive {
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
		if err := sender.Send(ctx, rec.ID, wakeMessageFor(rec.Kind, now)+lostJobsSentence(jobs), nil); err != nil {
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
