package turnnotify

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/procmem"
)

const (
	// DefaultNudgeDelay is how long a worker stays idle after its turn, with
	// no report and no running background job, before Tallpine nudges it.
	DefaultNudgeDelay = 10 * time.Minute
	// DefaultNudgePoll re-checks a worker whose background job still runs.
	DefaultNudgePoll = 30 * time.Second
	// DefaultNudgeMaxWait stops waiting on a job that never ends (a dev
	// server left running, say); the orchestrator already heard turn_ended.
	DefaultNudgeMaxWait = 3 * time.Hour

	// maxNudgesPerEpisode bounds nudges until the worker reports or another
	// party gives it work, so an agent that keeps going quiet is not
	// re-prompted forever.
	maxNudgesPerEpisode = 1
)

// Nudger writes a Tallpine-initiated message to a live agent. It reports whether
// the message was sent; a session waiting on its user refuses it.
type Nudger interface {
	Nudge(ctx context.Context, id domain.SessionID, message string) (bool, error)
}

// Jobs counts a session's running background jobs.
type Jobs interface {
	RunningJobs(ctx context.Context, rec domain.SessionRecord) (int, error)
}

func (o *Observer) nudging() bool { return o.NudgeDelay > 0 && o.Nudger != nil }

// nudgeDue runs when a silent worker's nudge delay (or a job re-check) is up.
func (o *Observer) nudgeDue(id domain.SessionID, gen uint64) {
	if !o.nudgeArmed(id, gen) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, ok, err := o.Sessions.GetSession(ctx, id)
	if err != nil || !ok || rec.IsTerminated || rec.Kind != domain.KindWorker ||
		rec.Activity.State != domain.ActivityIdle {
		// Working again, waiting on its user, or gone: none is a silent idle.
		return
	}
	if o.Jobs != nil {
		if n, jobsErr := o.Jobs.RunningJobs(ctx, rec); jobsErr == nil && n > 0 {
			o.waitOnJob(id, gen)
			return
		}
	}
	o.mu.Lock()
	t := o.turns[id]
	if o.closed || t == nil || t.gen != gen || t.reported || t.nudged {
		o.mu.Unlock()
		return
	}
	t.nudged, t.nudgeTimer = true, nil
	waitedOnJob := t.waitedOnJob
	// Count the nudge before sending: the turn it opens can start (and must
	// find nudgePending) before Nudge returns.
	t.nudges++
	t.nudgePending = true
	o.mu.Unlock()

	sent, err := o.Nudger.Nudge(ctx, id, NudgeMessage(waitedOnJob, o.NudgeDelay))
	if err != nil {
		o.Log.Warn("idle worker nudge failed", "session", id, "err", err)
	}
	if !sent {
		o.mu.Lock()
		if t := o.turns[id]; t != nil && t.nudgePending {
			t.nudges--
			t.nudgePending = false
		}
		o.mu.Unlock()
	}
}

func (o *Observer) nudgeArmed(id domain.SessionID, gen uint64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := o.turns[id]
	return !o.closed && t != nil && t.gen == gen && !t.reported && !t.nudged
}

// waitOnJob re-arms the check while the worker's own background job runs,
// so the nudge lands when the job it waits on finishes.
func (o *Observer) waitOnJob(id domain.SessionID, gen uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := o.turns[id]
	if o.closed || t == nil || t.gen != gen || t.reported || t.nudged {
		return
	}
	now := o.Now()
	if t.waitingSince.IsZero() {
		t.waitingSince = now
	}
	if now.Sub(t.waitingSince) >= o.NudgeMaxWait {
		t.nudgeTimer = nil
		return
	}
	t.waitedOnJob = true
	t.nudgeTimer = o.AfterFunc(o.NudgePoll, func() { o.nudgeDue(id, gen) })
}

// NudgeMessage is the short prompt Tallpine sends a silent idle worker.
func NudgeMessage(jobFinished bool, idle time.Duration) string {
	if jobFinished {
		return "[auto] idle check: the background job you were waiting on has finished. " +
			"Check its result and continue the task. When the task is done, blocked, or needs a decision, run `tallpine report`."
	}
	return fmt.Sprintf("[auto] idle check: your turn ended over %s ago and no `tallpine report` was filed. "+
		"If you are waiting on something, check it now and continue. When the task is done, blocked, or needs a decision, run `tallpine report`.",
		idle.Round(time.Minute))
}

// ProcessJobs counts background jobs in a session's process tree: shell
// processes started below a non-shell process (the agent), which is how agent
// tools run commands. The pane's own login shell has no agent above it, and
// an agent's direct non-shell helpers (MCP servers) are not jobs.
type ProcessJobs struct {
	// Roots names where the session's process tree starts.
	Roots func(context.Context, domain.SessionRecord) []int
	// Snapshot reads the process table; nil uses procmem.Snapshot.
	Snapshot func(context.Context) (*procmem.Table, error)
	// CacheTTL shares one process-table read between sessions checked together.
	CacheTTL time.Duration

	mu       sync.Mutex
	cached   *procmem.Table
	cachedAt time.Time
}

// RunningJobs implements Jobs. A session whose tree cannot be read reports
// none, so a probe failure never suppresses the nudge forever.
func (p *ProcessJobs) RunningJobs(ctx context.Context, rec domain.SessionRecord) (int, error) {
	if p.Roots == nil {
		return 0, nil
	}
	roots := p.Roots(ctx, rec)
	if len(roots) == 0 {
		return 0, nil
	}
	table, err := p.table(ctx)
	if err != nil {
		return 0, err
	}
	return countShellJobs(table.Tree(roots...).Processes), nil
}

func (p *ProcessJobs) table(ctx context.Context) (*procmem.Table, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && time.Since(p.cachedAt) < p.CacheTTL {
		return p.cached, nil
	}
	snapshot := p.Snapshot
	if snapshot == nil {
		snapshot = func(ctx context.Context) (*procmem.Table, error) { return procmem.Snapshot(ctx, nil) }
	}
	table, err := snapshot(ctx)
	if err != nil {
		return nil, err
	}
	p.cached, p.cachedAt = table, time.Now()
	return table, nil
}

func countShellJobs(processes []procmem.Process) int {
	byPID := make(map[int]procmem.Process, len(processes))
	for _, process := range processes {
		byPID[process.PID] = process
	}
	jobs := 0
	for _, process := range processes {
		if !isShell(process.Command) {
			continue
		}
		parent, ok := byPID[process.PPID]
		for hops := 0; ok && hops < len(processes); hops++ {
			if !isShell(parent.Command) {
				jobs++
				break
			}
			parent, ok = byPID[parent.PPID]
		}
	}
	return jobs
}

func isShell(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	switch filepath.Base(strings.TrimPrefix(fields[0], "-")) {
	case "sh", "bash", "zsh", "dash", "fish", "ksh":
		return true
	}
	return false
}
