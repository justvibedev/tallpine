package review

import (
	stdctx "context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ItemSource returns the live backlog item a worker was started on, read at
// call time so edits made after the worker started are not lost (B601).
type ItemSource interface {
	LinkedItemForWorker(ctx stdctx.Context, id domain.SessionID) (domain.BacklogItem, bool, error)
}

// taskTextFor is the task text a reviewer is shown. The stored prompt is the
// brief plus a trailing backlog section captured at spawn; that section goes
// stale after `backlog edit`, so it is replaced with the item's current text
// and the brief is kept. Without an item, a lookup failure or a prompt with no
// backlog section it falls back to the stored prompt.
func (e *Engine) taskTextFor(ctx stdctx.Context, worker domain.SessionRecord) string {
	prompt := worker.Metadata.Prompt
	if e.items == nil {
		return prompt
	}
	at := strings.LastIndex(prompt, domain.BacklogPromptMarker)
	if at < 0 {
		return prompt
	}
	it, ok, err := e.items.LinkedItemForWorker(ctx, worker.ID)
	if err != nil || !ok {
		return prompt
	}
	return prompt[:at] + domain.BacklogPromptHeader + it.TaskContext()
}
