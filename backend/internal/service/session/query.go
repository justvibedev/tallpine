package session

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// List returns sessions as enriched display models after applying API filters.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]domain.Session, error) {
	page, err := s.ListPage(ctx, filter)
	if err != nil {
		return nil, err
	}
	return page.Sessions, nil
}

// ListPage lists the sessions matching filter, one page at a time when
// filter.Limit is set, and reports the archive an active-only list left out.
// The page is chosen from the stored rows before any read-model facts are
// loaded, so a page of an archive of thousands costs a page's worth of reads.
func (s *Service) ListPage(ctx context.Context, filter ListFilter) (ListPage, error) {
	recoveryRevision := s.statusRecoveryRevision()
	if filter.Limit < 0 || filter.Limit > MaxListLimit {
		return ListPage{}, apierr.Invalid("INVALID_SESSION_LIMIT", fmt.Sprintf("Session list limit must be between 1 and %d", MaxListLimit), nil)
	}
	var after *listPageKey
	if filter.Cursor != "" {
		if filter.Limit == 0 {
			return ListPage{}, invalidSessionCursor()
		}
		key, err := decodeSessionCursor(filter.Cursor)
		if err != nil {
			return ListPage{}, err
		}
		after = &key
	}
	matched, archive, err := s.selectRecords(ctx, filter)
	if err != nil {
		return ListPage{}, err
	}
	if filter.OrchestratorOnly {
		if matched, err = s.dropEndedLaneOrchestrators(ctx, matched); err != nil {
			return ListPage{}, err
		}
	}
	page := ListPage{Archived: archive}
	if filter.Limit > 0 {
		matched, page.NextCursor = pageSessionRecords(matched, after, filter.Limit)
	}
	page.Sessions, err = s.sessionsFromRecords(ctx, matched, recoveryRevision)
	if err != nil {
		return ListPage{}, err
	}
	if filter.OrchestratorOnly {
		markCurrentOrchestrators(page.Sessions)
	}
	return page, nil
}

// sessionsFromRecords builds the read model for already-selected rows, batching
// every per-session fact read so the cost does not grow with the row count.
func (s *Service) sessionsFromRecords(ctx context.Context, filtered []domain.SessionRecord, recoveryRevision uint64) ([]domain.Session, error) {
	ids := make([]domain.SessionID, 0, len(filtered))
	var archived []domain.SessionID
	for _, rec := range filtered {
		ids = append(ids, rec.ID)
		if rec.IsTerminated {
			archived = append(archived, rec.ID)
		}
	}
	prsBySession, err := s.store.ListPRFactsForSessions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list pr facts: %w", err)
	}
	runsBySession, err := s.store.ListCurrentHeadReviewRunsForSessions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list review runs: %w", err)
	}
	interrupted, err := s.store.ListInterruptedSessions(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list interrupted sessions: %w", err)
	}
	var cleanupBySession map[domain.SessionID]domain.SessionCleanupRecord
	if len(archived) > 0 {
		if cleanupBySession, err = s.store.ListSessionCleanupFactsForSessions(ctx, archived); err != nil {
			return nil, fmt.Errorf("list workspace cleanup facts: %w", err)
		}
	}
	needsInput := s.loadNeedsInput(ctx, filtered)
	laneLabels := s.loadLaneLabels(ctx, filtered)
	branchRuns := s.loadBranchReviewRuns(ctx, filtered)
	out := make([]domain.Session, 0, len(filtered))
	for _, rec := range filtered {
		facts, found := cleanupBySession[rec.ID]
		sess, err := s.toSessionWithFacts(ctx, rec, prsBySession[rec.ID], runsBySession[rec.ID], interrupted[rec.ID], currentWorkspaceCleanup(rec, facts, found), false, needsInput, laneLabels, branchRuns[rec.ID])
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	if s.statusRecoveryRevision() != recoveryRevision {
		for i := range out {
			out[i].StatusReadiness = "checking"
		}
	}
	return out, nil
}

func (s *Service) statusRecoveryRevision() uint64 {
	if recovery, ok := s.manager.(interface{ StatusRecoveryRevision() uint64 }); ok {
		return recovery.StatusRecoveryRevision()
	}
	return 0
}

// selectRecords picks the stored rows a list shows. An active-only list reads
// just the live rows and counts the archive in SQL, so the list the app polls
// costs O(live sessions) rather than decoding every session ever recorded.
func (s *Service) selectRecords(ctx context.Context, filter ListFilter) ([]domain.SessionRecord, *ArchiveSummary, error) {
	if filter.Active == nil || !*filter.Active {
		recs, err := s.listRecords(ctx, filter.ProjectID)
		if err != nil {
			return nil, nil, err
		}
		matched, archive := selectSessionRecords(recs, filter)
		return matched, archive, nil
	}
	live, err := s.store.ListLiveSessions(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list live sessions: %w", err)
	}
	counts, err := s.store.CountArchivedSessions(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("count archived sessions: %w", err)
	}
	matched, archive := selectLiveSessionRecords(live, counts, filter)
	return matched, archive, nil
}

func (s *Service) listRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if project == "" {
		recs, err := s.store.ListAllSessions(ctx)
		if err != nil {
			return nil, fmt.Errorf("list all sessions: %w", err)
		}
		return recs, nil
	}
	recs, err := s.store.ListSessions(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", project, err)
	}
	return recs, nil
}

func matchesSessionFilter(rec domain.SessionRecord, filter ListFilter) bool {
	if rec.IsTaskPreparation {
		return false
	}
	if filter.Active != nil && rec.IsTerminated == *filter.Active {
		return false
	}
	switch filter.Kind {
	case domain.KindOrchestrator:
		if rec.Kind != domain.KindOrchestrator {
			return false
		}
	case domain.KindWorker:
		if rec.Kind == domain.KindOrchestrator {
			return false
		}
	}
	if filter.OrchestratorOnly && rec.Kind != domain.KindOrchestrator {
		return false
	}
	if filter.Fresh && rec.IsTerminated {
		return false
	}
	if filter.Standalone && rec.ProjectID != "" {
		return false
	}
	return true
}

// GetRecord reads a session's stored row without the read model Get builds on
// top (PR facts, review runs, artifact listing, agent switch). It serves hot
// paths that need only stored fields, such as the preview proxy, which runs
// once for every module a dev server serves.
func (s *Service) GetRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok || rec.IsTaskPreparation {
		return domain.SessionRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return rec, nil
}

// Get returns one session as an enriched display model, or an apierr.NotFound
// (SESSION_NOT_FOUND) if it is absent.
func (s *Service) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	recoveryRevision := s.statusRecoveryRevision()
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.IsTaskPreparation {
		return domain.Session{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	sess, err := s.toSession(ctx, rec)
	if err != nil {
		return domain.Session{}, err
	}
	if s.statusRecoveryRevision() != recoveryRevision {
		sess.StatusReadiness = "checking"
	}
	return sess, nil
}
