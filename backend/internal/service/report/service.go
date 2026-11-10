package report

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// Store is the report service's persistence and ownership boundary.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	CreateReport(context.Context, domain.ReportRecord) (domain.ReportRecord, error)
}

// CreateInput is the validated caller payload before Tallpine-derived ownership and deadlines.
type CreateInput struct {
	// TurnFallbackID is used only by the daemon completion observer.
	TurnFallbackID string
	// ID, when set, makes creation idempotent: a report with this ID that
	// already exists is not stored again, and Create returns an error wrapping
	// domain.ErrDuplicateReport. Empty assigns a fresh ID.
	ID        string
	SessionID domain.SessionID
	State     domain.ReportState
	Note      string
	Message   string
	Outputs   []domain.ReportOutput
	// Immediate is retained for caller compatibility and ignored. Reports are
	// immediately eligible except Quiet ones and checkpoints without outputs.
	Immediate bool
	// Synthesized marks a daemon-authored notice about the worker rather than
	// a report by it, so it does not count as the worker having reported.
	Synthesized bool
	// Quiet files a non-actionable FYI that does not wake the orchestrator at
	// once: it waits for its bounded deadline (Deps.QuietDelay), unless the next
	// actionable batch or the orchestrator's next chat turn carries it sooner.
	Quiet bool
	// QuietFor shortens a Quiet report's wait below the service quiet delay (never longer).
	QuietFor time.Duration
	// Defer holds the report's `--defer` lines (already limited by the caller's
	// validation, re-checked here). Each becomes an inbox backlog item.
	Defer []string
}

// DefaultQuietDelay bounds how long a quiet report waits with no other traffic.
var DefaultQuietDelay = 10 * time.Minute

// ArtifactRecorder shows a reported artifact in the worker's chat thread. It
// is best effort: it skips what it cannot show and never fails the report.
type ArtifactRecorder interface {
	RecordReportedArtifact(ctx context.Context, id domain.SessionID, reference string)
}

// Service validates and creates durable worker reports.
type Service struct {
	store       Store
	now         func() time.Time
	newID       func() string
	quietDelay  time.Duration
	batchWindow time.Duration
	onCreated   func(domain.ReportRecord)
	artifacts   ArtifactRecorder
	refresh     func(context.Context, domain.SessionID) error
	claimPR     func(context.Context, domain.SessionID, string) error
	digestCount func(context.Context, domain.ProjectID) int
	fileDefer   func(context.Context, domain.ProjectID, domain.SessionID, string, []string) ([]domain.DeferOutcome, error)
}

// Deps configures a Service. Artifacts and RefreshBranchState are optional.
type Deps struct {
	Store Store
	Now   func() time.Time
	NewID func() string
	// QuietDelay bounds how long a Quiet report waits alone (default DefaultQuietDelay).
	QuietDelay time.Duration
	// BatchWindow delays a non-urgent done or checkpoint report so close-together
	// reports share one wake-up. Zero delivers immediately.
	BatchWindow time.Duration
	OnCreated   func(domain.ReportRecord)
	Artifacts   ArtifactRecorder
	// RefreshBranchState re-reads a session's branch facts from its worktree
	// (local git only) so done verification does not depend on the SCM tick.
	RefreshBranchState func(context.Context, domain.SessionID) error
	// ClaimReportedPR attaches a PR a worker reported as created to that
	// worker. The PR must be open and in the project's repository, and it is
	// linked only when no other session owns it, terminated owners included: a
	// report never takes a PR over. It is best effort and never fails the report.
	ClaimReportedPR func(ctx context.Context, id domain.SessionID, prURL string) error
	// DigestCount returns the project's quiet-report count threshold (0 = off).
	// Optional; with it and a store that can flush, a new quiet report that
	// brings the waiting quiet reports to the threshold releases them together.
	DigestCount func(context.Context, domain.ProjectID) int
	// FileDeferred turns a report's `--defer` lines into inbox backlog items.
	// Optional; without it deferred lines are reported as not filed.
	FileDeferred func(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID, excerpt string, lines []string) ([]domain.DeferOutcome, error)
}

// New constructs a report Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func() string { return "rpt_" + uuid.NewString() }
	}
	if d.QuietDelay <= 0 {
		d.QuietDelay = DefaultQuietDelay
	}
	return &Service{quietDelay: d.QuietDelay, batchWindow: d.BatchWindow, store: d.Store, now: d.Now, newID: d.NewID, onCreated: d.OnCreated, artifacts: d.Artifacts, refresh: d.RefreshBranchState, claimPR: d.ClaimReportedPR, digestCount: d.DigestCount, fileDefer: d.FileDeferred}
}

// ListProject returns persisted report facts ordered by created_at then id,
// with each report's outputs in persisted position order. It never claims,
// acknowledges, schedules, or otherwise mutates report delivery state.
func (s *Service) ListProject(ctx context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("report: store is required")
	}
	if projectID == "" {
		return nil, apierr.Invalid("REPORT_PROJECT_REQUIRED", "Project id is required", nil)
	}
	reader, ok := s.store.(interface {
		ListReportsByProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error)
	})
	if !ok {
		return nil, errors.New("report: project reader is required")
	}
	reports, err := reader.ListReportsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].CreatedAt.Equal(reports[j].CreatedAt) {
			return reports[i].ID < reports[j].ID
		}
		return reports[i].CreatedAt.Before(reports[j].CreatedAt)
	})
	return reports, nil
}

// Create validates ownership and persists one pending report. Reports are
// coordination claims and never mutate authoritative worker lifecycle state.
func (s *Service) Create(ctx context.Context, input CreateInput) (domain.ReportRecord, error) {
	if s == nil || s.store == nil {
		return domain.ReportRecord{}, errors.New("report: store is required")
	}
	if input.SessionID == "" {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT_SESSION", "Worker session id is required", nil)
	}
	if err := domain.ValidateReportContent(input.State, input.Note, input.Message, input.Outputs); err != nil {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT", "Report state, note, message, or outputs are invalid", nil)
	}
	deferLines, err := domain.ValidateDeferLines(input.Defer)
	if err != nil {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_DEFER", "At most 3 --defer lines per report, each 1-200 characters", nil)
	}
	session, ok, err := s.store.GetSession(ctx, input.SessionID)
	if err != nil {
		return domain.ReportRecord{}, err
	}
	if !ok {
		return domain.ReportRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if session.Kind != domain.KindWorker {
		return domain.ReportRecord{}, apierr.Invalid("REPORT_WORKER_REQUIRED", "Reports can only be created by worker sessions", nil)
	}

	now := s.now().UTC()
	var settlementDeadline time.Time
	if input.State == domain.ReportDone {
		// Keep the observer grace for missing-PR verification separate from
		// delivery eligibility. Local facts can still be checked immediately.
		settlementDeadline = now.Add(domain.ReportSettlementWindow)
	}
	id := input.ID
	if id == "" {
		id = s.newID()
	}
	rec := domain.ReportRecord{
		ID: id, SessionID: session.ID, ProjectID: session.ProjectID, TurnFallbackID: input.TurnFallbackID,
		State: input.State, Note: input.Note, Message: input.Message, Outputs: input.Outputs,
		CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now,
		SettlementDeadline: settlementDeadline, RepeatCount: 1, Synthesized: input.Synthesized,
	}
	if !input.Synthesized && (input.State == domain.ReportDone || input.State == domain.ReportCheckpoint) {
		rec.AvailableAt = now.Add(s.batchWindow)
	}
	if input.Quiet || quietCheckpoint(input) {
		wait := s.quietDelay
		if input.QuietFor > 0 {
			wait = min(wait, input.QuietFor)
		}
		rec.AvailableAt = now.Add(wait)
		rec.Quiet = true
	}
	created := rec
	if session.IsStandalone() {
		// A projectless session has no orchestrator to deliver to, and reports
		// are keyed by project, so there is nothing durable to store. Accept the
		// report so the agent's call succeeds; its artifacts still reach the chat.
		created.DeliveryState = domain.ReportAcknowledged
		for _, l := range deferLines {
			created.Deferred = append(created.Deferred, domain.DeferOutcome{Line: l, Status: domain.DeferFailed, Reason: "not filed: no project"})
		}
	} else {
		var err error
		// Claim before storing so a PR the session could not link (another
		// session's, closed, mistyped) is never presented as this worker's (B324).
		if !rec.Synthesized {
			var refused []string
			rec, refused = dropUnlinkedPRs(rec, s.claimReportedPRs(ctx, session, rec.Outputs))
			if len(refused) > 0 && rec.State == "" && rec.Message == "" && len(rec.Outputs) == 0 {
				// An outputs-only report whose PRs were all refused has nothing left to say.
				return domain.ReportRecord{}, apierr.Invalid("REPORT_PR_NOT_LINKABLE", "Reported PR is not this session's PR and could not be linked: "+strings.Join(refused, ", "), nil)
			}
		}
		created, err = s.store.CreateReport(ctx, rec)
		if err != nil {
			return created, err
		}
		// The store persists the flag; this keeps the record complete for stores that do not.
		created.Synthesized = rec.Synthesized
		// Before onCreated, so the orchestrator never hears of the PR before
		// Tallpine tracks it: a review trigger right after the report must find it.
		// A daemon-authored notice is not the worker reporting a PR.
		if !created.Synthesized {
			created.Deferred = s.fileDeferred(ctx, session, deferLines, rec)
		}
		if created.Quiet {
			s.flushDigest(ctx, created.ProjectID)
		}
		if s.onCreated != nil {
			s.onCreated(created)
		}
	}
	// Synchronous on purpose: tallpine report --done is often the agent's last tool
	// call, so a goroutine would race the turn's completion and could drop the
	// artifact or put it on the next turn.
	if s.artifacts != nil {
		for _, output := range created.Outputs {
			if output.Kind == domain.ReportOutputArtifact {
				s.artifacts.RecordReportedArtifact(ctx, created.SessionID, output.Reference)
			}
		}
	}
	return created, nil
}

// fileDeferred files the report's `--defer` lines as inbox backlog items. It
// is best effort and never fails the report: when filing cannot happen, every
// line is reported back as not filed so the worker can put it in its note.
func (s *Service) fileDeferred(ctx context.Context, session domain.SessionRecord, lines []string, rec domain.ReportRecord) []domain.DeferOutcome {
	if len(lines) == 0 {
		return nil
	}
	failed := func(reason string) []domain.DeferOutcome {
		out := make([]domain.DeferOutcome, len(lines))
		for i, l := range lines {
			out[i] = domain.DeferOutcome{Line: l, Status: domain.DeferFailed, Reason: reason}
		}
		return out
	}
	if s.fileDefer == nil {
		return failed("not filed: backlog unavailable")
	}
	excerpt := rec.Note
	if excerpt == "" {
		excerpt = rec.Message
	}
	out, err := s.fileDefer(ctx, session.ProjectID, session.ID, excerpt, lines)
	if err != nil {
		return failed("not filed: backlog unavailable")
	}
	return out
}

// reportedPRClaimTimeout bounds the provider read a reported-PR claim waits for.
const reportedPRClaimTimeout = 10 * time.Second

// ErrPRNotLinkable marks a claim refused because the PR is not this session's
// (another owner, another session's branch, or not open). Only it drops a
// reported PR from the stored report.
var ErrPRNotLinkable = errors.New("report: PR cannot be linked to this session")

// claimReportedPRs links each PR the report says the worker created to the
// worker when Tallpine does not track it on that session yet. The SCM observer finds
// such PRs only on its next poll, and only when the head branch follows the
// session's naming; until then `tallpine review trigger` had nothing to review. The
// claim is synchronous so the link exists when the report call returns.
func (s *Service) claimReportedPRs(ctx context.Context, session domain.SessionRecord, outputs []domain.ReportOutput) (unlinked map[string]bool) {
	if s.claimPR == nil || session.IsTerminated {
		return nil
	}
	var tracked []domain.PullRequest
	listed := false
	for _, o := range outputs {
		ref := strings.TrimSpace(o.Reference)
		if o.Kind != domain.ReportOutputPRCreated || ref == "" {
			continue
		}
		if !listed {
			listed = true
			if lister, ok := s.store.(prLister); ok {
				tracked, _ = lister.ListPRsBySession(ctx, session.ID)
			}
		}
		if prTracked(tracked, ref) {
			continue
		}
		claimCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportedPRClaimTimeout)
		err := s.claimPR(claimCtx, session.ID, ref)
		cancel()
		if err != nil {
			slog.Default().InfoContext(ctx, "report: reported PR not linked to session", "session", session.ID, "pr", ref, "err", err)
			if unlinked == nil {
				unlinked = map[string]bool{}
			}
			if errors.Is(err, ErrPRNotLinkable) {
				unlinked[ref] = true
			}
		}
	}
	return unlinked
}

// dropUnlinkedPRs removes pr_created outputs that could not be linked to the
// reporting session and says so in the note, so neither the stored report nor
// the orchestrator notice names a foreign PR as the worker's.
func dropUnlinkedPRs(rec domain.ReportRecord, unlinked map[string]bool) (domain.ReportRecord, []string) {
	if len(unlinked) == 0 {
		return rec, nil
	}
	kept := make([]domain.ReportOutput, 0, len(rec.Outputs))
	var notices, refused []string
	for _, o := range rec.Outputs {
		ref := strings.TrimSpace(o.Reference)
		if o.Kind == domain.ReportOutputPRCreated && unlinked[ref] {
			refused = append(refused, ref)
			notices = append(notices, "(reported PR "+ref+" is not this session's PR, not linked)")
			continue
		}
		kept = append(kept, o)
	}
	if len(notices) == 0 {
		return rec, nil
	}
	rec.Outputs = kept
	notice := strings.Join(notices, " ")
	if r := []rune(notice); len(r) > domain.MaxReportTextCharacters {
		notice = string(r[:domain.MaxReportTextCharacters])
	}
	if rec.State == "" {
		// A stateless report carries no note; Create rejects it if nothing is left.
		return rec, refused
	}
	// Keep the note inside the stored limit: shorten the worker's text, not the notice.
	if room := domain.MaxReportTextCharacters - utf8.RuneCountInString(notice) - 1; room < 1 {
		rec.Note = notice
	} else {
		if r := []rune(rec.Note); len(r) > room {
			rec.Note = string(r[:room])
		}
		rec.Note = strings.TrimSpace(rec.Note) + " " + notice
	}
	return rec, refused
}

func prTracked(prs []domain.PullRequest, ref string) bool {
	for _, pr := range prs {
		if pr.URL == ref || pr.HTMLURL == ref {
			return true
		}
	}
	return false
}

// prLister and projectReader are optional Store capabilities. A store without
// them simply skips done verification.
type prLister interface {
	ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error)
}

type projectReader interface {
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
}

// UnverifiedDoneReason soft-verifies a done report at delivery time. Initial
// delivery is eligible immediately; only missing-PR verification waits for the
// settlement grace, and applies if delivery happens after that grace.
// The session's branch facts are refreshed from its worktree first, so the
// check never reads a stale or not-yet-observed branch. It returns "" for any
// non-done report or lookup failure.
func (s *Service) UnverifiedDoneReason(ctx context.Context, r domain.ReportRecord) string {
	if s == nil || s.store == nil || r.State != domain.ReportDone {
		return ""
	}
	if s.refresh != nil {
		refreshCtx, cancel := context.WithTimeout(ctx, branchRefreshTimeout)
		_ = s.refresh(refreshCtx, r.SessionID) // best effort: a failure leaves the last observed facts
		cancel()
	}
	session, ok, err := s.store.GetSession(ctx, r.SessionID)
	if err != nil || !ok {
		return ""
	}
	settled := !r.SettlementDeadline.IsZero() && !s.now().Before(r.SettlementDeadline)
	return s.unverifiedDoneReason(ctx, session, r.Outputs, settled)
}

// branchRefreshTimeout bounds the worktree read done verification waits for.
const branchRefreshTimeout = 5 * time.Second

// unverifiedDoneReason soft-checks a done report against already-tracked PR and
// branch facts and returns why the work looks undelivered, or "" when it looks
// delivered or the check does not apply. It never fails the report and makes no
// network calls: any lookup error means "no annotation".
//
// Unpushed commits are a local git fact and are checked on every delivery.
// "No pull request" depends on the SCM observer having seen a PR opened moments
// ago, so it is only claimed once the settlement window has given the observer
// time (settled); before that a missing PR is unknown, not evidence.
//
// There is no durable review-only flag, so a report that lists a reviewed PR
// stands in for review-only work.
func (s *Service) unverifiedDoneReason(ctx context.Context, session domain.SessionRecord, outputs []domain.ReportOutput, settled bool) string {
	if session.ProjectID == "" || session.RequestedOutput == domain.RequestedOutputArtifact || session.OutputType == domain.SessionOutputArtifact {
		return ""
	}
	for _, o := range outputs {
		switch o.Kind {
		case domain.ReportOutputPRCreated, domain.ReportOutputPRReviewed, domain.ReportOutputArtifact:
			return ""
		}
	}
	projects, ok := s.store.(projectReader)
	prs, ok2 := s.store.(prLister)
	if !ok || !ok2 {
		return ""
	}
	project, found, err := projects.GetProject(ctx, string(session.ProjectID))
	if err != nil || !found || project.RepoOriginURL == "" {
		return ""
	}
	branch := session.BranchState
	if branch == nil {
		return "" // branch never observed: unknown, not evidence of undelivered work
	}
	if branch.Unpushed > 0 {
		return fmt.Sprintf("%d unpushed commit(s)", branch.Unpushed)
	}
	if !settled {
		return ""
	}
	list, err := prs.ListPRsBySession(ctx, session.ID)
	if err != nil {
		return ""
	}
	if len(list) > 0 {
		return ""
	}
	if branch.Commits == 0 {
		return "" // no code changes, so there is nothing to put in a PR
	}
	return "no pull request recorded for this session"
}

// quietCheckpoint reports whether a checkpoint is progress only: it does not
// wake the orchestrator by itself (B192). A checkpoint that carries any output
// (artifact or PR) stays immediate, since outputs are reviewable deliverables.
func quietCheckpoint(input CreateInput) bool {
	return input.State == domain.ReportCheckpoint && len(input.Outputs) == 0
}
