package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// CommitQueuedTurnReports transfers a claimed report batch into its durable
// consuming prompt atomically. A crash cannot acknowledge an absent prompt or
// replay an already attached batch after RequeueClaimedReports.
func (s *Store) CommitQueuedTurnReports(ctx context.Context, conversationID, turnID string, message domain.ConversationMessage, projectID domain.ProjectID, claimToken string, count int, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "attach queued turn reports", func(q *gen.Queries) error {
		changed, err := q.UpdateQueuedConversationMessageText(ctx, gen.UpdateQueuedConversationMessageTextParams{
			Text: message.Text, DeliveryContentJson: message.DeliveryContentJSON,
			Revision: message.Revision, UpdatedAt: now, ConversationID: conversationID,
			TurnID: sql.NullString{String: turnID, Valid: true},
		})
		if err != nil {
			return err
		}
		if changed != 1 {
			return ErrQueuedTurnNotAvailable
		}
		acked, err := q.AcknowledgeReportBatch(ctx, gen.AcknowledgeReportBatchParams{
			ProjectID: string(projectID), ClaimToken: claimToken, AcknowledgedAt: nullTime(now),
		})
		if err != nil {
			return err
		}
		if count == 0 || len(acked) != count {
			return errors.New("report claim changed before prompt attachment")
		}
		return nil
	})
}

// CreateReport atomically persists one validated pending report and all outputs.
func (s *Store) CreateReport(ctx context.Context, rec domain.ReportRecord) (domain.ReportRecord, error) {
	if err := rec.Validate(); err != nil {
		return domain.ReportRecord{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var row gen.Report
	err := s.inTx(ctx, "create report", func(q *gen.Queries) error {
		if rec.TurnFallbackID != "" {
			if err := supersedeInvalidTurnFallbacks(ctx, q); err != nil {
				return err
			}
			changed, err := q.EmitTurnFallback(ctx, gen.EmitTurnFallbackParams{ID: rec.TurnFallbackID, DueAt: nullTime(rec.CreatedAt)})
			if err != nil {
				return err
			}
			if changed != 1 {
				return domain.ErrTurnFallbackInactive
			}
		}
		if err := suppressReportFallback(ctx, q, rec); err != nil {
			return err
		}
		var err error
		row, err = q.CreateReport(ctx, gen.CreateReportParams{
			ID: rec.ID, SessionID: string(rec.SessionID), ProjectID: string(rec.ProjectID),
			State: string(rec.State), Note: rec.Note, Message: rec.Message,
			CreatedAt: rec.CreatedAt, AvailableAt: rec.AvailableAt,
			SettlementDeadline: nullTime(rec.SettlementDeadline), RepeatCount: rec.RepeatCount,
			Synthesized: boolInt(rec.Synthesized),
		})
		if err != nil {
			return err
		}
		if rec.Quiet {
			if err := q.MarkReportQuiet(ctx, rec.ID); err != nil {
				return err
			}
		}
		for i, output := range rec.Outputs {
			if err := q.CreateReportOutput(ctx, gen.CreateReportOutputParams{
				ReportID: rec.ID, Position: int64(i), Kind: string(output.Kind),
				Reference: output.Reference, Label: output.Label,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if isSQLiteUnique(err) || isSQLitePrimaryKey(err) {
		return domain.ReportRecord{}, fmt.Errorf("create report %s: %w", rec.ID, domain.ErrDuplicateReport)
	}
	if err != nil {
		return domain.ReportRecord{}, fmt.Errorf("create report %s: %w", rec.ID, err)
	}
	created := reportFromGen(row)
	created.Outputs = append([]domain.ReportOutput(nil), rec.Outputs...)
	return created, nil
}

// GetReport returns one report by durable ID with its ordered outputs.
func (s *Store) GetReport(ctx context.Context, id string) (domain.ReportRecord, bool, error) {
	row, err := s.qr.GetReport(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReportRecord{}, false, nil
	}
	if err != nil {
		return domain.ReportRecord{}, false, fmt.Errorf("get report %s: %w", id, err)
	}
	rec, err := s.reportWithOutputs(ctx, row)
	if err != nil {
		return domain.ReportRecord{}, false, err
	}
	return rec, true, nil
}

// ListReportsBySession returns a worker's reports in creation order.
func (s *Store) ListReportsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListReportsBySession(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ListReportedPRURLs returns only a worker's structured created-PR outputs.
func (s *Store) ListReportedPRURLs(ctx context.Context, id domain.SessionID) ([]string, error) {
	urls, err := s.qr.ListReportedPRURLs(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list reported PR URLs for %s: %w", id, err)
	}
	return urls, nil
}

// ListReportsByProject is the read-only persisted report projection. Consumers
// such as Project Summary do not participate in the delivery claim lifecycle.
func (s *Store) ListReportsByProject(ctx context.Context, id domain.ProjectID) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListReportsByProject(ctx, string(id))
	if err != nil {
		return nil, fmt.Errorf("list project reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ReportBatchOpen reports whether any report of the batch is still pending or
// claimed, without loading report history.
func (s *Store) ReportBatchOpen(ctx context.Context, id domain.ProjectID, batchID string) (bool, error) {
	open, err := s.qr.ReportBatchOpen(ctx, gen.ReportBatchOpenParams{ProjectID: string(id), DeliveryBatchID: batchID})
	if err != nil {
		return false, fmt.Errorf("check report batch %s: %w", batchID, err)
	}
	return open, nil
}

// ListPendingReportSchedule returns each project's earliest pending report,
// ordered by availability, regardless of availability so a coordinator can
// restore the original deadlines after restart.
func (s *Store) ListPendingReportSchedule(ctx context.Context, limit int64) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListPendingReportSchedule(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list report schedule: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ClaimPendingReportBatch atomically claims every pending report of one lane
// ("" = un-laned) of a project. A due, attention, or piggyback trigger therefore includes earlier batch work.
func (s *Store) ClaimPendingReportBatch(ctx context.Context, projectID domain.ProjectID, lane, token string, at time.Time) ([]domain.ReportRecord, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return nil, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows []gen.Report
	err := s.inTx(ctx, "claim project report batch", func(q *gen.Queries) error {
		scheduled, err := q.ListPendingReportsByProjectSchedule(ctx, gen.ListPendingReportsByProjectScheduleParams{
			ProjectID: string(projectID), LaneItemID: lane, Limit: 1,
		})
		if err != nil || len(scheduled) == 0 {
			return err
		}
		batchID := scheduled[0].DeliveryBatchID
		if batchID == "" {
			batchID = "report-batch:" + scheduled[0].ID
			if err := q.AssignPendingReportsToBatch(ctx, gen.AssignPendingReportsToBatchParams{
				DeliveryBatchID: batchID, ProjectID: string(projectID), LaneItemID: lane,
			}); err != nil {
				return err
			}
		}
		rows, err = q.ClaimPendingReportsByBatch(ctx, gen.ClaimPendingReportsByBatchParams{
			ProjectID: string(projectID), DeliveryBatchID: batchID,
			ClaimToken: token, ClaimedAt: nullTime(at),
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("claim project report batch: %w", err)
	}
	return s.reportsWithOutputs(ctx, orderedReportRows(rows))
}

// SweepFreshLaneReports claims, under token and into batchID, the never-attempted
// unbatched pending reports of another lane that resolves to the same
// orchestrator. Retry batches keep their own identity and are never merged.
func (s *Store) SweepFreshLaneReports(ctx context.Context, projectID domain.ProjectID, lane, batchID, token string, at time.Time) ([]domain.ReportRecord, error) {
	if projectID == "" || batchID == "" || token == "" || at.IsZero() {
		return nil, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SweepFreshLaneReportsIntoBatch(ctx, gen.SweepFreshLaneReportsIntoBatchParams{
		ProjectID: string(projectID), LaneItemID: lane, DeliveryBatchID: batchID,
		ClaimToken: token, ClaimedAt: nullTime(at),
	})
	if err != nil {
		return nil, fmt.Errorf("sweep fresh lane reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, orderedReportRows(rows))
}

// AcknowledgeReportBatch completes only the project batch holding token.
func (s *Store) AcknowledgeReportBatch(ctx context.Context, projectID domain.ProjectID, token string, at time.Time) (int, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.AcknowledgeReportBatch(ctx, gen.AcknowledgeReportBatchParams{
		ProjectID: string(projectID), ClaimToken: token, AcknowledgedAt: nullTime(at),
	})
	if err != nil {
		return 0, fmt.Errorf("acknowledge project report batch: %w", err)
	}
	return len(rows), nil
}

// ReleaseReportBatch returns the matching project batch to pending without
// moving any report's original deadline later.
func (s *Store) ReleaseReportBatch(ctx context.Context, projectID domain.ProjectID, token string, at time.Time, lastError string) (int, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.ReleaseReportBatch(ctx, gen.ReleaseReportBatchParams{
		ProjectID: string(projectID), ClaimToken: token, AvailableAt: at, LastError: lastError,
	})
	if err != nil {
		return 0, fmt.Errorf("release project report batch: %w", err)
	}
	return len(rows), nil
}

// DeferReportBatch coalesces a throttled urgent retry until it may request the
// next interruption. Unlike failure release, this intentionally moves its
// delivery availability later.
func (s *Store) DeferReportBatch(ctx context.Context, projectID domain.ProjectID, token string, at time.Time, lastError string) (int, error) {
	if projectID == "" || token == "" || at.IsZero() {
		return 0, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.DeferReportBatch(ctx, gen.DeferReportBatchParams{
		ProjectID: string(projectID), ClaimToken: token, AvailableAt: at, LastError: lastError,
	})
	if err != nil {
		return 0, fmt.Errorf("defer project report batch: %w", err)
	}
	return len(rows), nil
}

// DropPendingProjectReports settles every pending report of a project without
// delivering it, keeping reason as the durable record. Claimed reports are
// mid-delivery and are left to finish.
func (s *Store) DropPendingProjectReports(ctx context.Context, projectID domain.ProjectID, at time.Time, reason string) ([]domain.ReportRecord, error) {
	if projectID == "" || at.IsZero() || strings.TrimSpace(reason) == "" {
		return nil, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.DropPendingProjectReports(ctx, gen.DropPendingProjectReportsParams{
		ProjectID: string(projectID), ClaimToken: domain.ReportDroppedClaimToken,
		DroppedAt: nullTime(at), LastError: reason,
	})
	if err != nil {
		return nil, fmt.Errorf("drop pending project reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, orderedReportRows(rows))
}

// DropClaimedReport settles one claimed report without delivering it, keeping
// reason as the durable record. It reports false when the claim no longer holds.
func (s *Store) DropClaimedReport(ctx context.Context, id, token string, at time.Time, reason string) (bool, error) {
	if id == "" || token == "" || at.IsZero() || strings.TrimSpace(reason) == "" {
		return false, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.qw.DropClaimedReport(ctx, gen.DropClaimedReportParams{
		DroppedToken: domain.ReportStaleClaimToken, DroppedAt: nullTime(at),
		LastError: reason, ID: id, ClaimToken: token,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("drop claimed report: %w", err)
	}
	return true, nil
}

// AcquireReportInterrupt durably enforces the per-worker interruption window.
func (s *Store) AcquireReportInterrupt(ctx context.Context, sessionID domain.SessionID, at time.Time, window time.Duration) (bool, time.Time, error) {
	if sessionID == "" || at.IsZero() || window <= 0 {
		return false, time.Time{}, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	acquired := false
	next := at
	err := s.inTx(ctx, "acquire report interrupt", func(q *gen.Queries) error {
		last, err := q.GetReportInterrupt(ctx, string(sessionID))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && last.After(at.Add(-window)) {
			next = last.Add(window)
			return nil
		}
		if err := q.PutReportInterrupt(ctx, gen.PutReportInterruptParams{SessionID: string(sessionID), LastInterruptedAt: at}); err != nil {
			return err
		}
		acquired = true
		return nil
	})
	return acquired, next, err
}

// ListPendingReports returns reports eligible for a later delivery batch.
func (s *Store) ListPendingReports(ctx context.Context, at time.Time, limit int64) ([]domain.ReportRecord, error) {
	rows, err := s.qr.ListPendingReports(ctx, gen.ListPendingReportsParams{AvailableAt: at, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list pending reports: %w", err)
	}
	return s.reportsWithOutputs(ctx, rows)
}

// ClaimReport atomically fences one pending report to a delivery attempt token.
func (s *Store) ClaimReport(ctx context.Context, id, token string, at time.Time) (domain.ReportRecord, bool, error) {
	return s.updateReportClaim(ctx, id, token, at, "claim", func() (gen.Report, error) {
		return s.qw.ClaimReport(ctx, gen.ClaimReportParams{ID: id, ClaimToken: token, ClaimedAt: nullTime(at)})
	})
}

// AcknowledgeReport completes only the delivery attempt holding the current token.
func (s *Store) AcknowledgeReport(ctx context.Context, id, token string, at time.Time) (domain.ReportRecord, bool, error) {
	return s.updateReportClaim(ctx, id, token, at, "acknowledge", func() (gen.Report, error) {
		return s.qw.AcknowledgeReport(ctx, gen.AcknowledgeReportParams{ID: id, ClaimToken: token, AcknowledgedAt: nullTime(at)})
	})
}

// ReleaseReport returns the matching token's claim to pending with retry metadata.
func (s *Store) ReleaseReport(ctx context.Context, id, token string, availableAt time.Time, lastError string) (domain.ReportRecord, bool, error) {
	return s.updateReportClaim(ctx, id, token, availableAt, "release", func() (gen.Report, error) {
		return s.qw.ReleaseReport(ctx, gen.ReleaseReportParams{ID: id, ClaimToken: token, AvailableAt: availableAt, LastError: lastError})
	})
}

func (s *Store) updateReportClaim(ctx context.Context, id, token string, at time.Time, action string, update func() (gen.Report, error)) (domain.ReportRecord, bool, error) {
	if id == "" || token == "" || at.IsZero() {
		return domain.ReportRecord{}, false, domain.ErrInvalidReport
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	row, err := update()
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ReportRecord{}, false, nil
	}
	if err != nil {
		return domain.ReportRecord{}, false, fmt.Errorf("%s report %s: %w", action, id, err)
	}
	rec, err := s.reportWithOutputs(ctx, row)
	return rec, err == nil, err
}

// RequeueClaimedReports atomically recovers claims owned by a prior daemon
// process and removes legacy initial batching delays. Attempted reports keep
// their retry deadlines; settlement verification deadlines are never changed.
func (s *Store) RequeueClaimedReports(ctx context.Context) (int64, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var count int64
	err := s.inTx(ctx, "recover report delivery", func(q *gen.Queries) error {
		var err error
		count, err = q.RequeueClaimedReports(ctx)
		if err != nil {
			return err
		}
		return q.ReleaseInitialReportDelays(ctx)
	})
	if err != nil {
		return 0, fmt.Errorf("requeue claimed reports: %w", err)
	}
	return count, nil
}

func (s *Store) reportWithOutputs(ctx context.Context, row gen.Report) (domain.ReportRecord, error) {
	outputs, err := s.qr.ListReportOutputs(ctx, row.ID)
	if err != nil {
		return domain.ReportRecord{}, fmt.Errorf("list report %s outputs: %w", row.ID, err)
	}
	rec := reportFromGen(row)
	rec.Outputs = make([]domain.ReportOutput, len(outputs))
	for i, output := range outputs {
		rec.Outputs[i] = domain.ReportOutput{Kind: domain.ReportOutputKind(output.Kind), Reference: output.Reference, Label: output.Label}
	}
	return rec, nil
}

func (s *Store) reportsWithOutputs(ctx context.Context, rows []gen.Report) ([]domain.ReportRecord, error) {
	out := make([]domain.ReportRecord, 0, len(rows))
	for _, row := range rows {
		rec, err := s.reportWithOutputs(ctx, row)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func orderedReportRows(rows []gen.Report) []gen.Report {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].CreatedAt.Before(rows[j].CreatedAt)
	})
	return rows
}

func reportFromGen(r gen.Report) domain.ReportRecord {
	return domain.ReportRecord{
		ID: r.ID, SessionID: domain.SessionID(r.SessionID), ProjectID: domain.ProjectID(r.ProjectID),
		State: domain.ReportState(r.State), Note: r.Note, Message: r.Message, CreatedAt: r.CreatedAt,
		DeliveryState: domain.ReportDeliveryState(r.DeliveryState), AvailableAt: r.AvailableAt,
		SettlementDeadline: timeFromNull(r.SettlementDeadline), RepeatCount: r.RepeatCount,
		ClaimToken: r.ClaimToken, ClaimedAt: timeFromNull(r.ClaimedAt), DeliveryAttempts: r.DeliveryAttempts,
		AcknowledgedAt: timeFromNull(r.AcknowledgedAt), LastError: r.LastError,
		DeliveryBatchID: r.DeliveryBatchID, Synthesized: r.Synthesized != 0,
	}
}

// ExpediteBusyReports releases only availability retries, including after restart.
// Failure backoff and repeated-stuck interruption deadlines remain untouched.
func (s *Store) ExpediteBusyReports(ctx context.Context, projectID domain.ProjectID, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ExpediteBusyReports(ctx, gen.ExpediteBusyReportsParams{
		ProjectID: string(projectID), AvailableAt: at, LastError: domain.ReportDeliveryBusyReason,
	})
}
