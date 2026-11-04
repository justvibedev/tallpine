package session

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// needsInputReportReader is the optional batched store read for the newest
// needs_input report of each listed session.
type needsInputReportReader interface {
	LatestNeedsInputReports(context.Context, []domain.SessionID) (map[domain.SessionID]domain.ReportRecord, error)
}

// needsInputBatch holds the facts behind the needs-input audience of the
// sessions of one read, loaded once so a list's cost does not grow with its rows.
type needsInputBatch struct {
	reports map[domain.SessionID]domain.ReportRecord
	failed  bool
	live    map[domain.ProjectID]bool
}

func (s *Service) loadNeedsInput(ctx context.Context, recs []domain.SessionRecord) *needsInputBatch {
	batch := &needsInputBatch{live: map[domain.ProjectID]bool{}}
	reader, ok := s.store.(needsInputReportReader)
	if !ok {
		return batch
	}
	ids := make([]domain.SessionID, 0, len(recs))
	for _, rec := range recs {
		if rec.Kind == domain.KindWorker && !rec.IsTerminated {
			ids = append(ids, rec.ID)
		}
	}
	var err error
	if batch.reports, err = reader.LatestNeedsInputReports(ctx, ids); err != nil {
		batch.failed = true
	}
	return batch
}

// audience derives who a worker's needs-input state is waiting on. A read
// failure degrades to the human: surfacing a state wrongly is cheaper than hiding one.
func (b *needsInputBatch) audience(ctx context.Context, s *Service, rec domain.SessionRecord, now time.Time) domain.NeedsInputAudience {
	if b == nil || rec.IsTerminated || rec.Kind != domain.KindWorker {
		return domain.NeedsInputNone
	}
	if b.failed {
		if rec.Activity.State.NeedsInput() {
			return domain.NeedsInputHuman
		}
		return domain.NeedsInputNone
	}
	report, hasReport := b.reports[rec.ID]
	live := false
	if hasReport {
		var known bool
		if live, known = b.live[rec.ProjectID]; !known {
			live = s.hasLiveOrchestrator(ctx, rec.ProjectID)
			b.live[rec.ProjectID] = live
		}
	}
	facts := domain.NewNeedsInputFacts(rec, report, live, now)
	return domain.DeriveNeedsInputAudience(facts)
}

// NeedsInputAudienceNow derives one session's audience from the service clock.
func (s *Service) NeedsInputAudienceNow(ctx context.Context, rec domain.SessionRecord) domain.NeedsInputAudience {
	return s.NeedsInputAudience(ctx, rec, s.now())
}

// NeedsInputAudience derives one session's audience at now.
func (s *Service) NeedsInputAudience(ctx context.Context, rec domain.SessionRecord, now time.Time) domain.NeedsInputAudience {
	return s.loadNeedsInput(ctx, []domain.SessionRecord{rec}).audience(ctx, s, rec, now)
}

// hasLiveOrchestrator mirrors the report coordinator's target choice: a
// non-terminated, non-exited orchestrator in the worker's project.
func (s *Service) hasLiveOrchestrator(ctx context.Context, project domain.ProjectID) bool {
	if project == "" {
		return false
	}
	recs, err := s.store.ListSessions(ctx, project)
	if err != nil {
		return false
	}
	for _, rec := range recs {
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated && rec.Activity.State != domain.ActivityExited {
			return true
		}
	}
	return false
}

// recordInteraction stamps the session as acted on (a claim, a review), which
// ends a wait on the orchestrator. Best effort: a failed stamp only leaves the
// wait to its timeout.
func (s *Service) recordInteraction(ctx context.Context, id domain.SessionID) {
	if recorder, ok := s.store.(ports.SessionInteractionRecorder); ok {
		if err := recorder.RecordSessionInteraction(ctx, id, "", s.now()); err != nil && s.logger != nil {
			s.logger.Warn("record session interaction", "session", id, "err", err)
		}
	}
}
