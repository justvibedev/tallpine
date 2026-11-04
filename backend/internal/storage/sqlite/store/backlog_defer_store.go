package store

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// BacklogDeferStatus is the outcome for one deferred line.
type BacklogDeferStatus string

// Deferred line outcomes.
const (
	BacklogDeferFiled      BacklogDeferStatus = "filed"
	BacklogDeferMerged     BacklogDeferStatus = "merged"
	BacklogDeferInboxFull  BacklogDeferStatus = "inbox_full"
	BacklogDeferSessionCap BacklogDeferStatus = "session_cap"
)

// BacklogDeferOutcome reports what happened to one `--defer` line.
type BacklogDeferOutcome struct {
	Line   string             `json:"line"`
	Status BacklogDeferStatus `json:"status"`
	Ref    string             `json:"ref,omitempty"`
}

// BacklogDeferInput describes one report's deferred lines.
type BacklogDeferInput struct {
	ProjectID domain.ProjectID
	SessionID string
	Lines     []string
	Excerpt   string
	// Note and DoneWhen fill the item body when a worker files through `backlog add`.
	Note, DoneWhen string
	Now            time.Time
	NewID          func() string
}

// FileDeferredBacklog turns a report's deferred lines into inbox items in one
// transaction. A line whose normalized title matches a live (inbox/open) item
// merges into it by linking the filing session; otherwise it files a new inbox
// item unless the session or project cap is reached.
func (s *Store) FileDeferredBacklog(ctx context.Context, in BacklogDeferInput) ([]BacklogDeferOutcome, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	project := string(in.ProjectID)
	out := make([]BacklogDeferOutcome, 0, len(in.Lines))
	for _, line := range in.Lines {
		live, err := q.SelectBacklogLiveItems(ctx, project)
		if err != nil {
			return nil, err
		}
		key := domain.NormalizeBacklogTitle(line)
		var existing *gen.BacklogItem
		for i := range live {
			if domain.NormalizeBacklogTitle(live[i].Title) == key {
				existing = &live[i]
				break
			}
		}
		if existing != nil {
			if _, err := q.InsertBacklogLink(ctx, gen.InsertBacklogLinkParams{
				ItemID: existing.ID, Kind: string(domain.BacklogLinkSession), Ref: in.SessionID, CreatedBy: in.SessionID, CreatedAt: in.Now,
			}); err != nil {
				return nil, err
			}
			if err := q.TouchBacklogItem(ctx, gen.TouchBacklogItemParams{UpdatedAt: in.Now, ID: existing.ID}); err != nil {
				return nil, err
			}
			out = append(out, BacklogDeferOutcome{Line: line, Status: BacklogDeferMerged, Ref: domain.FormatBacklogRef(existing.Seq)})
			continue
		}
		filed, err := q.CountBacklogItemsByOriginSession(ctx, gen.CountBacklogItemsByOriginSessionParams{ProjectID: project, OriginSessionID: in.SessionID})
		if err != nil {
			return nil, err
		}
		if filed >= domain.MaxDeferPerSession {
			out = append(out, BacklogDeferOutcome{Line: line, Status: BacklogDeferSessionCap})
			continue
		}
		inbox, err := q.CountBacklogInbox(ctx, project)
		if err != nil {
			return nil, err
		}
		if inbox >= domain.MaxBacklogInboxPerProject {
			out = append(out, BacklogDeferOutcome{Line: line, Status: BacklogDeferInboxFull})
			continue
		}
		item, err := insertBacklogItem(ctx, q, domain.BacklogItem{
			ID: in.NewID(), ProjectID: in.ProjectID, Title: line, Note: in.Note, DoneWhen: in.DoneWhen, State: domain.BacklogInbox,
			Origin:    domain.BacklogOrigin{Kind: domain.BacklogOriginWorker, SessionID: in.SessionID, Excerpt: in.Excerpt},
			CreatedAt: in.Now, UpdatedAt: in.Now,
		})
		if err != nil {
			return nil, fmt.Errorf("file deferred line: %w", err)
		}
		if _, err := q.InsertBacklogLink(ctx, gen.InsertBacklogLinkParams{
			ItemID: item.ID, Kind: string(domain.BacklogLinkSession), Ref: in.SessionID, CreatedBy: in.SessionID, CreatedAt: in.Now,
		}); err != nil {
			return nil, err
		}
		out = append(out, BacklogDeferOutcome{Line: line, Status: BacklogDeferFiled, Ref: item.Ref()})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
