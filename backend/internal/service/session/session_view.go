package session

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// toSessionWithFacts builds the read model from already-loaded facts.
// withArtifacts walks the session's artifact directory to list its files; the
// list endpoint passes false, because walking every session's directory on
// every request cost O(all sessions x artifact files) and dominated the
// payload. A list row still reports hasArtifacts from the persisted output
// type, and GET /sessions/{id} (withArtifacts) serves the files.
func (s *Service) toSessionWithFacts(ctx context.Context, rec domain.SessionRecord, prs []domain.PRFacts, runs []domain.CurrentHeadReviewRun, interrupted bool, cleanup domain.WorkspaceDisposition, withArtifacts bool, needsInput *needsInputBatch, lanes *laneLabelBatch) (domain.Session, error) {
	// A row created before artifact_dir existed carries it as '' (the
	// migration's default) even though session_manager always prompts the
	// agent to write into the deterministic dataDir/artifacts/<id> path.
	// Fall back to that derived path here so the file listing and preview
	// root (sess.Metadata.ArtifactDir, read directly by the preview
	// controller) are correct on this read, rather than waiting for
	// lifecycle.Manager.ReconcileSessionOutputType's next poll tick to
	// persist the backfill.
	//
	// The artifact-output poller skips terminated sessions (nothing more can
	// happen to a session that is done), so a terminated row would never get
	// durably repaired through that path alone. Trigger the same reconcile
	// here, on read, unconditionally of IsTerminated, in two cases: a legacy
	// row whose ArtifactDir was empty, and a row whose persisted type does not
	// yet record artifact files that exist on disk (the agent wrote output and
	// the session ended before any reconcile ran). Both are one-time
	// self-healing writes, not a recurring per-read cost.
	backfilledArtifactDir := false
	if rec.Metadata.ArtifactDir == "" {
		if dir := sessionartifacts.Dir(s.dataDir, rec.ID); dir != "" {
			rec.Metadata.ArtifactDir = dir
			backfilledArtifactDir = true
		}
	}
	var artifactFiles []domain.SessionArtifactFile
	if withArtifacts {
		var err error
		// Cached: this read runs for every session change event the renderer
		// applies, and for every artifact file the daemon serves.
		artifactFiles, err = sessionartifacts.ListCached(ctx, rec.Metadata.ArtifactDir)
		if err != nil {
			// An unwalkable artifact root must not fail the session read (and with
			// it the whole board); the artifact list is simply empty for this read.
			if s.logger != nil {
				s.logger.Warn("list session artifacts", "session", rec.ID, "err", err)
			}
			artifactFiles = nil
		}
		if backfilledArtifactDir || (len(artifactFiles) > 0) != rec.OutputType.HasArtifact() {
			// Reflect the repair in this response's OutputType too, not just
			// future ones: the persisted write below lands asynchronously
			// relative to this read.
			rec.OutputType = sessionartifacts.DeriveOutputType(len(prs), len(artifactFiles))
			if s.outputTypeReconciler != nil {
				if err := s.outputTypeReconciler.ReconcileSessionOutputType(ctx, rec.ID); err != nil && s.logger != nil {
					s.logger.Warn("reconcile output type on read", "session", rec.ID, "err", err)
				}
			}
		}
	}
	runs = canonicalizeCurrentHeadReviewRuns(prs, runs)
	prs = deduplicatePRFacts(prs)
	// Both derivations read the clock once, from the same instant: they share
	// the no-signal rule, and two reads could put them either side of its grace
	// period and have the card contradict its own status.
	now := s.now()
	// rec.OutputType is the persisted column (lifecycle.Manager.
	// ReconcileSessionOutputType is the sole writer); it is deliberately not
	// recomputed here so the API and Kanban see the same durable
	// classification rather than a value that can differ read to read.
	presentation := deriveKanbanPresentation(rec, prs, runs, interrupted, now, s.harnessSignals(rec.Harness))
	readiness := "ready"
	if recovery, ok := s.manager.(interface {
		SessionStatusReadiness(domain.SessionRecord) string
	}); ok {
		readiness = recovery.SessionStatusReadiness(rec)
	}
	unsupportedMessage := ""
	if rec.Harness.IsUnsupported() {
		unsupportedMessage = domain.UnsupportedHarnessMessage(rec.Harness)
	}
	return domain.Session{
		SessionRecord:             rec,
		Lane:                      lanes.label(rec.ID),
		WorkerLane:                lanes.workerLabel(rec.ID),
		WorkspaceCleanup:          cleanup,
		UnsupportedHarnessMessage: unsupportedMessage,
		StatusReadiness:           readiness,
		ChatProviderPreserved: rec.Mode == domain.SessionModeChat && !rec.IsTerminated &&
			s.chatProviderPreserved != nil && s.chatProviderPreserved(rec.ID),
		Status:             deriveStatus(rec, prs, now, s.harnessSignals(rec.Harness)),
		NeedsInputAudience: needsInput.audience(ctx, s, rec, now),
		SCMStatus:          deriveSCMStatus(prs),
		KanbanColumn:       presentation.Column,
		DisplayStatus:      presentation.DisplayStatus,
		TerminalHandleID:   rec.Metadata.RuntimeHandleID,
		ArtifactFiles:      artifactFiles,
		PRs:                prs,
	}, nil
}

func (s *Service) toSession(ctx context.Context, rec domain.SessionRecord) (domain.Session, error) {
	prs, err := s.store.ListPRFactsForSession(ctx, rec.ID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("pr facts %s: %w", rec.ID, err)
	}
	runs, err := s.currentHeadReviewRuns(ctx, rec, prs)
	if err != nil {
		return domain.Session{}, err
	}
	var cleanup domain.WorkspaceDisposition
	if rec.IsTerminated {
		facts, found, err := s.store.GetSessionCleanupFacts(ctx, rec.ID)
		if err != nil {
			return domain.Session{}, fmt.Errorf("get workspace cleanup facts: %w", err)
		}
		cleanup = currentWorkspaceCleanup(rec, facts, found)
	}
	interrupted, err := s.store.ListInterruptedSessions(ctx, []domain.SessionID{rec.ID})
	if err != nil {
		return domain.Session{}, fmt.Errorf("interrupted turn %s: %w", rec.ID, err)
	}
	return s.toSessionWithFacts(ctx, rec, prs, runs, interrupted[rec.ID], cleanup, true, s.loadNeedsInput(ctx, []domain.SessionRecord{rec}), s.loadLaneLabels(ctx, []domain.SessionRecord{rec}))
}

// currentWorkspaceCleanup is the teardown disposition recorded for an archived
// session, provided the facts row belongs to its current cleanup generation.
func currentWorkspaceCleanup(rec domain.SessionRecord, facts domain.SessionCleanupRecord, found bool) domain.WorkspaceDisposition {
	if found && facts.SessionGeneration == rec.CleanupGeneration {
		return facts.WorkspaceDisposition
	}
	return ""
}

// currentHeadReviewRuns reads the session's Tallpine review passes for the Kanban
// reducer. Sessions the reducer already decides without them — terminated ones
// and ones with no PR yet — skip the query, so listing a board of building
// workers stays one read per session.
func (s *Service) currentHeadReviewRuns(ctx context.Context, rec domain.SessionRecord, prs []domain.PRFacts) ([]domain.CurrentHeadReviewRun, error) {
	if rec.IsTerminated || len(prs) == 0 {
		return nil, nil
	}
	runs, err := s.store.ListCurrentHeadReviewRunsForSession(ctx, rec.ID)
	if err != nil {
		return nil, fmt.Errorf("review runs %s: %w", rec.ID, err)
	}
	return runs, nil
}
