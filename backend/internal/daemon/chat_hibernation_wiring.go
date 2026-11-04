package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/procmem"
)

type chatHibernationSettings interface {
	ChatHibernation(context.Context) domain.ChatHibernationPolicy
}

type chatHibernationProjects interface {
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
}

// chatHibernationPolicy resolves when one idle Chat session may sleep: the
// app-wide setting, its kind's quiet threshold and its project's opt-out. An
// unreadable project keeps the session awake.
func chatHibernationPolicy(settings chatHibernationSettings, projects chatHibernationProjects) func(context.Context, domain.SessionRecord) (time.Duration, bool) {
	return func(ctx context.Context, rec domain.SessionRecord) (time.Duration, bool) {
		policy := settings.ChatHibernation(ctx)
		if !policy.Enabled {
			return 0, false
		}
		project, found, err := projects.GetProject(ctx, string(rec.ProjectID))
		if err != nil || !found || project.Config.ChatHibernationDisabled() {
			return 0, false
		}
		return policy.IdleFor(rec.Kind), true
	}
}

var errChatHostNotRunning = errors.New("chat provider host is not running")

// chatBackgroundProcesses reports whether a Chat session's agent runs work of
// its own: any process under its provider host outside the host's and the
// provider's process groups (see procmem.DetachedDescendants). A host that
// cannot be found is an error, which the chat service treats as busy.
//
// Windows hosts own their tree through a job object rather than process
// groups, so there the provider's own background-task check stands alone.
//
// The processes found are logged at debug level, so "why won't this agent
// sleep" has an answer: a tool that starts a detached child on purpose (a
// browser launched by an MCP server, say) keeps its agent awake too.
func chatBackgroundProcesses(dataDir string, snapshot func(context.Context) (*procmem.GroupTable, error), log *slog.Logger) func(context.Context, domain.SessionID) (bool, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if snapshot == nil {
		snapshot = func(ctx context.Context) (*procmem.GroupTable, error) {
			return procmem.SnapshotGroups(ctx, nil)
		}
	}
	return func(ctx context.Context, id domain.SessionID) (bool, error) {
		pid, ok := persistenthost.HostPID(dataDir, string(id))
		if !ok {
			return false, errChatHostNotRunning
		}
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		table, err := snapshot(ctx)
		if err != nil {
			return false, err
		}
		if !table.Alive(pid) {
			return false, errChatHostNotRunning
		}
		work := table.DetachedDescendants(pid)
		if len(work) > 0 && log.Enabled(ctx, slog.LevelDebug) {
			shown := work[:min(len(work), 5)]
			procs := make([]string, 0, len(shown))
			for _, p := range shown {
				procs = append(procs, fmt.Sprintf("%d %s", p, table.Command(p)))
			}
			log.Debug("chat agent kept awake by background processes", "session", id, "count", len(work), "processes", procs)
		}
		return len(work) > 0, nil
	}
}

type agentParker interface {
	ParkAll(context.Context)
}

// parkAgents stops every Chat agent before a deliberate quit, then ends any
// provider host still running (reviewers, side chats, a host whose controller
// was never attached), so nothing outlives the daemon. The hosts' own
// sessions are not terminated: sleeping ones resume on demand, reviewers are
// recovered by the next daemon.
func parkAgents(chat agentParker, dataDir string, timeout time.Duration, log *slog.Logger) {
	// Every provider stops in parallel; give a loaded machine at least as long
	// as the desktop waits for this daemon to exit on its own.
	timeout = max(timeout, 15*time.Second)
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	chat.ParkAll(ctx)
	if err := persistenthost.Reconcile(ctx, dataDir, nil); err != nil {
		log.Warn("some chat hosts did not stop on quit", "err", err)
	}
	log.Info("agents stopped for quit", "elapsed", time.Since(started).Round(time.Millisecond))
}
