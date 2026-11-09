package restartresume

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	lostJobsFileName = "restart-lost-jobs.json"
	// maxNamedJobs and maxJobNameRunes keep the notice readable when an agent
	// had many jobs or a job's name is a long command line.
	maxNamedJobs    = 20
	maxJobNameRunes = 120
)

// JobLister reads the background tasks a session's harness is running. The
// in-memory bgtasks.Tracker provides it; its contents die with the daemon, so
// they are copied to disk at shutdown.
type JobLister func(domain.SessionID) []bgtasks.Info

// lostJobs is one session's jobs as they stood at the last shutdown.
type lostJobs struct {
	At   time.Time `json:"at"`
	Jobs []lostJob `json:"jobs"`
}

type lostJob struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}

// WithJobs sets where shutdown reads each session's running jobs from.
func (t *Tracker) WithJobs(jobs JobLister) *Tracker {
	t.jobs = jobs
	return t
}

// recordJobs saves every chat session's running jobs ahead of a shutdown. It
// runs for any shutdown, because a bare one can still lose the host (the app
// stops the agents afterwards); RecordUnclean discards the entries of hosts
// that turn out to have survived. A session with no running jobs keeps an
// earlier undelivered entry, which is still true and still unsaid.
func (t *Tracker) recordJobs(ctx context.Context) error {
	if t.jobs == nil {
		return nil
	}
	recs, err := t.store.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.loadLostJobs()
	now := t.clock().UTC()
	for _, rec := range recs {
		if !resumable(rec.Kind) || rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
			continue
		}
		running := t.jobs(rec.ID)
		if len(running) == 0 {
			continue
		}
		jobs := make([]lostJob, 0, len(running))
		for _, info := range running {
			jobs = append(jobs, lostJob{Name: info.Name, Kind: info.Kind})
		}
		all[rec.ID] = lostJobs{At: now, Jobs: jobs}
	}
	slog.Default().InfoContext(ctx, "restart resume: shutdown saved running background jobs", "sessions", len(all))
	t.saveLostJobs(all)
	return nil
}

// dropSurvivedJobs forgets the jobs of sessions whose host outlived the
// restart: those jobs are still running and must not be reported as killed.
// Caller holds t.mu.
func (t *Tracker) dropSurvivedJobs(survived func(domain.SessionID) bool) {
	if survived == nil {
		return
	}
	all := t.loadLostJobs()
	changed := false
	for id := range all {
		if survived(id) {
			delete(all, id)
			changed = true
		}
	}
	if changed {
		t.saveLostJobs(all)
	}
}

// pruneLostJobs drops entries too old to act on and those of sessions that no
// longer exist. Caller holds t.mu.
func (t *Tracker) pruneLostJobs(recs []domain.SessionRecord) {
	live := map[domain.SessionID]bool{}
	for _, rec := range recs {
		if !rec.IsTerminated {
			live[rec.ID] = true
		}
	}
	all := t.loadLostJobs()
	changed := false
	for id, entry := range all {
		if !live[id] || t.clock().Sub(entry.At) > maxAge {
			delete(all, id)
			changed = true
		}
	}
	if changed {
		t.saveLostJobs(all)
	}
}

// lostJobsFor returns the session's undelivered jobs, ignoring an expired entry.
func (t *Tracker) lostJobsFor(id domain.SessionID) []lostJob {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry, ok := t.loadLostJobs()[id]
	if !ok || t.clock().Sub(entry.At) > maxAge {
		return nil
	}
	return entry.Jobs
}

// forgetLostJobs drops a session's entry once its agent has been told.
func (t *Tracker) forgetLostJobs(id domain.SessionID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	all := t.loadLostJobs()
	if _, ok := all[id]; ok {
		delete(all, id)
		t.saveLostJobs(all)
	}
}

// lostJobsSentence names the killed jobs for the agent. Empty for no jobs.
func lostJobsSentence(jobs []lostJob) string {
	if len(jobs) == 0 {
		return ""
	}
	shown := jobs[:min(len(jobs), maxNamedJobs)]
	parts := make([]string, 0, len(shown))
	for _, job := range shown {
		part := fmt.Sprintf("%q", jobDisplayName(job.Name))
		if job.Kind != "" {
			part += " (" + job.Kind + ")"
		}
		parts = append(parts, part)
	}
	list := strings.Join(parts, ", ")
	if extra := len(jobs) - len(shown); extra > 0 {
		list += fmt.Sprintf(" and %d more", extra)
	}
	return " Killed by the restart and no longer running: " + list + ". Start again any you still need."
}

// jobDisplayName flattens a harness-supplied name onto one bounded line.
func jobDisplayName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > maxJobNameRunes {
		return string(runes[:maxJobNameRunes]) + "…"
	}
	return name
}

// resumeJobsNotice is what a resumed agent is told about its jobs. A chat agent
// whose jobs are unknown still gets the generic warning, since the tracker
// cannot see jobs that a host adopted from an earlier daemon. A TUI agent's
// terminal host outlives the quit, so nothing is claimed for it.
func resumeJobsNotice(rec domain.SessionRecord, jobs []lostJob) string {
	if len(jobs) > 0 {
		return lostJobsSentence(jobs)
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeChat {
		return ""
	}
	return " Any background jobs or monitors you had running were stopped too; start again any you still need."
}

func (t *Tracker) loadLostJobs() map[domain.SessionID]lostJobs {
	out := map[domain.SessionID]lostJobs{}
	if raw, err := os.ReadFile(filepath.Join(filepath.Dir(t.path), lostJobsFileName)); err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func (t *Tracker) saveLostJobs(all map[domain.SessionID]lostJobs) {
	p := filepath.Join(filepath.Dir(t.path), lostJobsFileName)
	if len(all) == 0 {
		_ = os.Remove(p)
		return
	}
	if raw, err := json.Marshal(all); err == nil && os.WriteFile(p+".tmp", raw, 0o600) == nil {
		_ = os.Rename(p+".tmp", p)
	}
}
