// Package bgtasks tracks the background tasks a chat agent's harness runs for
// it (background Bash commands, Monitor watches) so the daemon can tell an
// agent that is waiting on one from an agent that is idle or stalled, and can
// wake the agent when the task it waits on ends.
//
// The harness normally wakes itself when a task finishes (it starts a "turn"
// task). The tracker only steps in when no such turn follows within a grace
// period and the session is idle, so a finished monitor can never leave a
// worker waiting forever. Task state lives in memory and is cleared when a
// session's harness is replaced (Clear). A harness that outlives a daemon
// restart (persistent host) keeps its tasks, which stay invisible here until
// they end; the restart wake skips those workers.
package bgtasks

import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// DefaultGrace is how long the harness gets to start its own turn after a task
// ends before the tracker wakes the agent.
const DefaultGrace = 20 * time.Second

// turnTaskType is the harness waking itself to work; the agent does not wait on it.
const turnTaskType = "turn"

// Sender delivers a message to a session's agent.
type Sender interface {
	Send(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment) error
}

// IdleFunc reports whether the session can take a continuation now (alive and
// not mid-turn).
type IdleFunc func(ctx context.Context, id domain.SessionID) bool

// Tracker is the per-daemon background task registry.
type Tracker struct {
	sender Sender
	idle   IdleFunc
	grace  time.Duration

	mu       sync.Mutex
	sessions map[domain.SessionID]*sessionTasks
}

type sessionTasks struct {
	running map[string]ports.ChatAsyncTask
	// ended remembers finished task ids so a repeated terminal report neither
	// wakes twice nor resurrects the task.
	ended map[string]bool
	// turnIDs are harness self-wake turn tasks. Only their spawn frame carries
	// taskType; progress and the end frame carry just the id.
	turnIDs map[string]bool
	// turns counts harness-started turns; a change since a task ended proves
	// the harness woke itself.
	turns int
	// stopRequested are tasks a person asked to stop; their end is not news the
	// agent needs a wake-up about.
	stopRequested map[string]bool
}

// New builds a tracker. A zero grace uses DefaultGrace.
func New(sender Sender, idle IdleFunc, grace time.Duration) *Tracker {
	if grace <= 0 {
		grace = DefaultGrace
	}
	return &Tracker{sender: sender, idle: idle, grace: grace, sessions: map[domain.SessionID]*sessionTasks{}}
}

// Apply records one task report for a session.
func (t *Tracker) Apply(id domain.SessionID, task ports.ChatAsyncTask) {
	if t == nil || task.ID == "" {
		return
	}
	t.mu.Lock()
	s := t.sessions[id]
	if s == nil {
		s = &sessionTasks{running: map[string]ports.ChatAsyncTask{}, ended: map[string]bool{}, turnIDs: map[string]bool{}}
		t.sessions[id] = s
	}
	if task.TaskType == turnTaskType && !s.turnIDs[task.ID] {
		s.turnIDs[task.ID] = true
		s.turns++
	}
	if s.turnIDs[task.ID] {
		if task.Done() {
			delete(s.turnIDs, task.ID)
		}
		t.mu.Unlock()
		return
	}
	if s.ended[task.ID] {
		t.mu.Unlock()
		return
	}
	if !task.Done() {
		prev := s.running[task.ID]
		if task.Name == "" {
			task.Name = prev.Name
		}
		if task.Description == "" {
			task.Description = prev.Description
		}
		if task.TaskType == "" {
			task.TaskType = prev.TaskType
		}
		s.running[task.ID] = task
		t.mu.Unlock()
		return
	}
	prev := s.running[task.ID]
	delete(s.running, task.ID)
	s.ended[task.ID] = true
	if task.Name == "" {
		task.Name = prev.Name
	}
	turnsAtEnd := s.turns
	// A turn that was open when the task ended, or a prompted turn still running,
	// receives the task's result itself; no continuation is needed.
	handled := len(s.turnIDs) > 0 || s.stopRequested[task.ID]
	delete(s.stopRequested, task.ID)
	t.mu.Unlock()
	if !handled && t.idle != nil && !t.idle(context.Background(), id) {
		handled = true
	}
	if handled {
		return
	}
	time.AfterFunc(t.grace, func() { t.wakeAfterEnd(id, task, turnsAtEnd) })
}

func (t *Tracker) wakeAfterEnd(id domain.SessionID, task ports.ChatAsyncTask, turnsAtEnd int) {
	t.mu.Lock()
	s := t.sessions[id]
	selfWoke := s == nil || s.turns != turnsAtEnd || len(s.turnIDs) > 0
	t.mu.Unlock()
	if selfWoke {
		return
	}
	ctx := context.Background()
	if t.idle != nil && !t.idle(ctx, id) {
		return
	}
	_ = t.sender.Send(ctx, id, WakeMessage(task), nil)
}

// Waiting lists the names of the tasks the session is waiting on, sorted.
func (t *Tracker) Waiting(id domain.SessionID) []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sessions[id]
	if s == nil || len(s.running) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.running))
	for _, task := range s.running {
		out = append(out, label(task))
	}
	sort.Strings(out)
	return out
}

// Info is one running background task, as a client lists it.
type Info struct {
	ID    string
	Name  string
	State string
}

// Running lists the session's running background tasks, sorted by id.
func (t *Tracker) Running(id domain.SessionID) []Info {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sessions[id]
	if s == nil || len(s.running) == 0 {
		return nil
	}
	out := make([]Info, 0, len(s.running))
	for _, task := range s.running {
		out = append(out, Info{ID: task.ID, Name: label(task), State: task.State})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ExpectStop records that a person asked to stop the task, so its end does not
// wake the agent to "continue". Call it before the stop is sent: the harness
// may report the end before the request returns. CancelStop undoes it when the
// stop did not happen.
func (t *Tracker) ExpectStop(id domain.SessionID, taskID string) {
	if t == nil || taskID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sessions[id]
	if s == nil {
		s = &sessionTasks{running: map[string]ports.ChatAsyncTask{}, ended: map[string]bool{}, turnIDs: map[string]bool{}}
		t.sessions[id] = s
	}
	if s.stopRequested == nil {
		s.stopRequested = map[string]bool{}
	}
	s.stopRequested[taskID] = true
}

// CancelStop withdraws ExpectStop for a stop that was refused or failed.
func (t *Tracker) CancelStop(id domain.SessionID, taskID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.sessions[id]; s != nil {
		delete(s.stopRequested, taskID)
	}
}

// Clear forgets a session's tasks, for when its harness stopped and took them along.
func (t *Tracker) Clear(id domain.SessionID) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.sessions, id)
	t.mu.Unlock()
}

func label(task ports.ChatAsyncTask) string {
	for _, v := range []string{task.Name, task.Description, task.ID} {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return task.ID
}

// WakeMessage is the continuation an agent gets when a task it waited on ended
// and its harness did not wake it.
func WakeMessage(task ports.ChatAsyncTask) string {
	msg := fmt.Sprintf("Background task %q ended (%s).", label(task), cmp.Or(task.State, "finished"))
	if s := strings.TrimSpace(task.Summary); s != "" {
		msg += " " + s
	}
	return msg + " Check its result and continue with your task."
}

// SendFunc adapts a function to Sender.
type SendFunc func(ctx context.Context, id domain.SessionID, message string) error

// Send implements Sender.
func (f SendFunc) Send(ctx context.Context, id domain.SessionID, message string, _ *ports.SpawnAttachment) error {
	return f(ctx, id, message)
}
