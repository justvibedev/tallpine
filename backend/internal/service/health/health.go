// Package health summarises silent failures: states where work is stuck but no
// error was ever shown. It only reads; it never repairs anything.
package health

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Grace is how long a condition may last before it counts as stuck.
const Grace = 2 * time.Minute

// Source reads the durable facts.
type Source interface {
	ListChatSessionsExpectedLive(ctx context.Context, settledBefore time.Time) ([]domain.SessionID, error)
	ListStuckReportBatches(ctx context.Context, deadline time.Time) ([]domain.StuckReportBatch, error)
	ListOverdueTurnFallbacks(ctx context.Context, deadline time.Time) ([]domain.OverdueTurnFallback, error)
}

// ChatLiveness reports whether a Chat session owns a live controller.
type ChatLiveness interface {
	HasLiveChatController(domain.SessionID) bool
}

// ReportParking exposes the report coordinator's in-memory parked and
// unconfirmed projects with the time each was first seen.
type ReportParking interface {
	UndeliverableProjects() map[domain.ProjectID]time.Time
}

// ChatIssue is a Chat session whose agent is not attached.
type ChatIssue struct {
	SessionID domain.SessionID
}

// ReportIssue is a project whose worker reports are not being delivered.
type ReportIssue struct {
	ProjectID     domain.ProjectID
	Pending       int
	Claimed       int
	Undeliverable bool // parked with no orchestrator, or delivery unconfirmed
	Since         time.Time
}

// FallbackIssue is a final-result notice pending past its due time.
type FallbackIssue struct {
	ID        string
	SessionID domain.SessionID
	DueAt     time.Time
}

// Status is the full summary. Every slice is non-nil.
type Status struct {
	ChatNotAttached []ChatIssue
	ReportBatches   []ReportIssue
	TurnFallbacks   []FallbackIssue
}

// Healthy reports whether no condition is present.
func (s Status) Healthy() bool {
	return len(s.ChatNotAttached) == 0 && len(s.ReportBatches) == 0 && len(s.TurnFallbacks) == 0
}

// SessionLookup resolves a session's project so a project-scoped caller sees
// only its own project's issues.
type SessionLookup interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// Service computes Status.
type Service struct {
	Source   Source
	Sessions SessionLookup
	Chat     ChatLiveness
	Reports  ReportParking
	Now      func() time.Time
	Log      *slog.Logger // nil uses slog.Default

	mu       sync.Mutex
	reported map[domain.SessionID]bool // unattached sessions already logged
}

// logUnattached writes one WARN per newly unattached session (with role and how
// long since its last update) so a recurrence leaves evidence; a session that
// recovers is forgotten so a later recurrence logs again.
func (s *Service) logUnattached(ctx context.Context, issues []ChatIssue, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := map[domain.SessionID]bool{}
	for _, c := range issues {
		current[c.SessionID] = true
		if s.reported[c.SessionID] {
			continue
		}
		role, since := "unknown", "unknown"
		if s.Sessions != nil {
			if rec, ok, err := s.Sessions.GetSession(ctx, c.SessionID); err == nil && ok {
				role = string(rec.Kind)
				if rec.Role != "" {
					role += "/" + string(rec.Role)
				}
				since = now.Sub(rec.UpdatedAt).Round(time.Second).String()
			}
		}
		log := s.Log
		if log == nil {
			log = slog.Default()
		}
		log.Warn("chat session has no attached agent", "sessionId", string(c.SessionID), "role", role, "sinceLastUpdate", since)
	}
	s.reported = current
}

// Status reads the three conditions.
func (s *Service) Status(ctx context.Context) (Status, error) {
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	cutoff := now.Add(-Grace)
	out := Status{ChatNotAttached: []ChatIssue{}, ReportBatches: []ReportIssue{}, TurnFallbacks: []FallbackIssue{}}

	ids, err := s.Source.ListChatSessionsExpectedLive(ctx, cutoff)
	if err != nil {
		return Status{}, err
	}
	for _, id := range ids {
		if s.Chat == nil || !s.Chat.HasLiveChatController(id) {
			out.ChatNotAttached = append(out.ChatNotAttached, ChatIssue{SessionID: id})
		}
	}

	s.logUnattached(ctx, out.ChatNotAttached, now)

	batches, err := s.Source.ListStuckReportBatches(ctx, cutoff)
	if err != nil {
		return Status{}, err
	}
	byProject := map[domain.ProjectID]int{}
	for _, b := range batches {
		out.ReportBatches = append(out.ReportBatches, ReportIssue{ProjectID: b.ProjectID, Pending: b.Pending, Claimed: b.Claimed, Since: b.Since})
		byProject[b.ProjectID] = len(out.ReportBatches) - 1
	}
	if s.Reports != nil {
		for id, since := range s.Reports.UndeliverableProjects() {
			if since.After(cutoff) {
				continue
			}
			if i, ok := byProject[id]; ok {
				out.ReportBatches[i].Undeliverable = true
				continue
			}
			out.ReportBatches = append(out.ReportBatches, ReportIssue{ProjectID: id, Undeliverable: true, Since: since})
		}
		sort.Slice(out.ReportBatches, func(i, j int) bool { return out.ReportBatches[i].ProjectID < out.ReportBatches[j].ProjectID })
	}

	fallbacks, err := s.Source.ListOverdueTurnFallbacks(ctx, cutoff)
	if err != nil {
		return Status{}, err
	}
	for _, f := range fallbacks {
		out.TurnFallbacks = append(out.TurnFallbacks, FallbackIssue{ID: f.ID, SessionID: f.SessionID, DueAt: f.DueAt})
	}
	return out, nil
}

// StatusForProject is Status limited to one project's issues. A session whose
// project cannot be resolved is dropped: least privilege over completeness.
func (s *Service) StatusForProject(ctx context.Context, project domain.ProjectID) (Status, error) {
	full, err := s.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{ChatNotAttached: []ChatIssue{}, ReportBatches: []ReportIssue{}, TurnFallbacks: []FallbackIssue{}}
	if project == "" {
		return out, nil // fail closed: an empty project must not match empty-project rows
	}
	owns := func(id domain.SessionID) (bool, error) {
		if s.Sessions == nil {
			return false, nil
		}
		rec, found, err := s.Sessions.GetSession(ctx, id)
		if err != nil {
			return false, err
		}
		return found && rec.ProjectID == project, nil
	}
	for _, c := range full.ChatNotAttached {
		ok, err := owns(c.SessionID)
		if err != nil {
			return Status{}, err
		}
		if ok {
			out.ChatNotAttached = append(out.ChatNotAttached, c)
		}
	}
	for _, b := range full.ReportBatches {
		if b.ProjectID == project {
			out.ReportBatches = append(out.ReportBatches, b)
		}
	}
	for _, f := range full.TurnFallbacks {
		ok, err := owns(f.SessionID)
		if err != nil {
			return Status{}, err
		}
		if ok {
			out.TurnFallbacks = append(out.TurnFallbacks, f)
		}
	}
	return out, nil
}
