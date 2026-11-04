package backlog

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// FileDeferred files a report's deferred lines as inbox items attributed to the
// session. Lines the caps refuse are reported with a reason, never silently
// dropped.
func (s *Service) FileDeferred(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID, excerpt string, lines []string) ([]domain.DeferOutcome, error) {
	lines, err := domain.ValidateDeferLines(lines)
	if err != nil || len(lines) == 0 {
		return nil, err
	}
	outcomes, err := s.store.FileDeferredBacklog(ctx, store.BacklogDeferInput{
		ProjectID: projectID, SessionID: string(sessionID), Lines: lines,
		Excerpt: truncateRunes(excerpt, domain.MaxBacklogExcerpt), Now: s.now().UTC(), NewID: s.newID,
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]domain.DeferOutcome, 0, len(outcomes))
	for _, o := range outcomes {
		do := domain.DeferOutcome{Line: o.Line, Status: domain.DeferStatus(o.Status), Ref: o.Ref}
		switch o.Status {
		case store.BacklogDeferInboxFull:
			do.Reason = "not filed: inbox cap reached"
		case store.BacklogDeferSessionCap:
			do.Reason = "not filed: per-session defer cap reached"
		}
		out = append(out, do)
	}
	return out, nil
}

// createAsAgent files a worker's (or any non-orchestrator agent's) item through
// the same capped, deduped path as `--defer`: one inbox line, merged into a
// matching live item, refused with a clear reason once a cap is reached. Agents
// cannot set a parent, hold or scope: those change what `next` returns.
func (s *Service) createAsAgent(ctx context.Context, projectID domain.ProjectID, actor Actor, in CreateInput) (View, error) {
	if in.ParentRef != "" || strings.TrimSpace(in.Hold) != "" || in.Scope != "" || in.AgentNotes != "" {
		return View{}, apierr.Invalid("BACKLOG_AGENT_FIELDS", "Agents file inbox items only: parent, hold, scope and agent notes are set by the orchestrator", nil)
	}
	title := strings.TrimSpace(in.Title)
	if err := validateItem(domain.BacklogItem{Title: title, Note: in.Note, DoneWhen: in.DoneWhen, Origin: domain.BacklogOrigin{Kind: actor.Kind}}); err != nil {
		return View{}, err
	}
	if _, err := domain.ValidateDeferLines([]string{title}); err != nil {
		return View{}, apierr.Invalid("INVALID_BACKLOG_TITLE", "Title is required and must be at most 200 characters", nil)
	}
	outcomes, err := s.store.FileDeferredBacklog(ctx, store.BacklogDeferInput{
		ProjectID: projectID, SessionID: actor.SessionID, Lines: []string{title},
		Excerpt: truncateRunes(in.Excerpt, domain.MaxBacklogExcerpt), Note: in.Note, DoneWhen: in.DoneWhen,
		Now: s.now().UTC(), NewID: s.newID,
	})
	if err != nil {
		return View{}, mapError(err)
	}
	o := outcomes[0]
	switch o.Status {
	case store.BacklogDeferInboxFull:
		return View{}, apierr.TooManyRequests("BACKLOG_INBOX_FULL", "not filed: inbox cap reached")
	case store.BacklogDeferSessionCap:
		return View{}, apierr.TooManyRequests("BACKLOG_SESSION_CAP", "not filed: per-session defer cap reached")
	}
	seq, _ := domain.ParseBacklogRef(o.Ref)
	item, ok, err := s.store.GetBacklogItemByRef(ctx, projectID, seq)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, apierr.NotFound("BACKLOG_ITEM_NOT_FOUND", "Unknown backlog item")
	}
	return s.view(ctx, item)
}
