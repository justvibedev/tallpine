package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// The pr / pr_checks / pr_comment rows are modelled by domain.PullRequest /
// domain.PullRequestCheck / domain.PullRequestComment — flat tables, one shared type per table.
// This layer only maps those to/from the sqlc gen.* params: the bool PR flags
// become the single pr.pr_state column, empty enums default to their
// "nothing known yet" value (matching the CHECK constraints), and ints widen to
// int64.

// Compile-time proof that *Store satisfies both ports it is wired into, so a
// drift between either interface and this implementation fails here at the point
// of definition rather than later at the call sites in lifecycle_wiring / tests.
var (
	_ ports.PRWriter  = (*Store)(nil)
	_ ports.SCMWriter = (*Store)(nil)
	_ ports.PRClaimer = (*Store)(nil)
)

// WritePR persists a legacy PR observation — scalar facts, check runs, and the
// replacement comment set — in one write transaction, so the rows and the
// change_log events their triggers emit are committed all-or-nothing. The scalar
// PR upsert runs first so the checks'/comments' CDC triggers can resolve the
// session id from the pr row within the same transaction. It intentionally does
// not touch pr_review_threads: those rows are owned by WriteSCMObservation's
// slower review-thread refresh path.
func (s *Store) WritePR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, comments []domain.PullRequestComment) error {
	return s.writePR(ctx, pr, checks, nil, nil, comments, ports.ReviewWritePreserve, true)
}

// WriteSCMObservation persists a provider-neutral SCM observation in one write
// transaction. It upserts the full PR metadata row and CI checks. Review threads
// and comments are preserved, replaced, or merged according to reviewMode
// because review polling runs at a slower and sometimes intentionally bounded
// cadence than metadata/CI polling.
func (s *Store) WriteSCMObservation(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode) error {
	return s.writePR(ctx, pr, checks, reviews, threads, comments, reviewMode, false)
}

// MarkPRMerged records the authoritative success returned by the provider's
// merge endpoint without replacing concurrently refreshed CI or review facts.
// The pr_cdc_update trigger emits the corresponding pr_updated event.
func (s *Store) MarkPRMerged(ctx context.Context, prURL, mergeCommitSHA string, mergedAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qr.MarkPRMerged(ctx, gen.MarkPRMergedParams{
		MergeCommitSha: mergeCommitSHA,
		StateChangedAt: sql.NullTime{Time: mergedAt, Valid: true},
		UpdatedAt:      mergedAt,
		ObservedAt:     sql.NullTime{Time: mergedAt, Valid: true},
		URL:            prURL,
	})
	if err != nil {
		return fmt.Errorf("mark pr merged %s: %w", prURL, err)
	}
	if rows != 1 {
		return fmt.Errorf("mark pr merged %s: updated %d rows", prURL, rows)
	}
	return nil
}

// ClaimPR moves (or creates) a PR row to pr.SessionID and applies the live SCM
// observation in the same transaction. The session_id update is what fires the
// pr_session_changed CDC trigger added in migration 0005.
func (s *Store) ClaimPR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode, allowActiveTakeover bool) (ports.ClaimOutcome, error) {
	return s.ClaimPRGuarded(ctx, pr, checks, reviews, threads, comments, reviewMode, allowActiveTakeover, nil)
}

// ClaimPRGuarded is ClaimPR with a takeover guard evaluated in the same
// transaction (see ports.GuardedPRClaimer).
func (s *Store) ClaimPRGuarded(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode, allowActiveTakeover bool, guard func(ports.ClaimOwner) error) (ports.ClaimOutcome, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	pr = normalizedPRIdentity(pr)
	var outcome ports.ClaimOutcome
	err := s.inTx(ctx, "claim pr", func(q *gen.Queries) error {
		var err error
		pr, err = resolveWeakPRAlias(ctx, q, pr)
		if err != nil {
			return err
		}
		candidates, err := findPRIdentityCandidates(ctx, q, pr)
		if err != nil {
			return err
		}
		for _, candidate := range candidates {
			owner, err := q.GetPRClaimAndOwner(ctx, candidate.URL)
			if err != nil {
				return err
			}
			if owner.SessionID == pr.SessionID {
				continue
			}
			if outcome.PreviousOwner != "" && outcome.PreviousOwner != owner.SessionID {
				return fmt.Errorf("pr identity resolves to multiple sessions: %s and %s", outcome.PreviousOwner, owner.SessionID)
			}
			outcome.PreviousOwner = owner.SessionID
			outcome.OwnerTerminated = owner.IsTerminated
			if guard != nil {
				row, err := q.GetSession(ctx, owner.SessionID)
				if err != nil {
					return fmt.Errorf("load pr owner %s: %w", owner.SessionID, err)
				}
				current := ports.ClaimOwner{SessionID: owner.SessionID, IsTerminated: owner.IsTerminated}
				if row.ProjectID != nil {
					current.ProjectID = *row.ProjectID
				}
				if err := guard(current); err != nil {
					return err
				}
			}
			if owner.SessionID != pr.SessionID && !owner.IsTerminated && !allowActiveTakeover {
				return ports.PRClaimedByActiveSessionError{Owner: owner.SessionID}
			}
		}
		if err := q.ClaimPRForSession(ctx, gen.ClaimPRForSessionParams{
			URL: pr.URL, SessionID: pr.SessionID, Number: int64(pr.Number), PRState: pr.State(),
			ReviewDecision: reviewOrDefault(pr.Review), CIState: ciOrDefault(pr.CI), Mergeability: mergeabilityOrDefault(pr.Mergeability), UpdatedAt: pr.UpdatedAt,
			StateChangedAt: nullTime(initialPRStateChangedAt(pr)), ID: pr.SessionID,
		}); err != nil {
			return err
		}
		return writePRRows(ctx, q, pr, checks, reviews, threads, comments, reviewMode, false, false)
	})
	return outcome, err
}

func (s *Store) writePR(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode, replaceLegacyComments bool) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "write pr observation", func(q *gen.Queries) error {
		return writePRRows(ctx, q, pr, checks, reviews, threads, comments, reviewMode, replaceLegacyComments, true)
	})
}

// GetPRLastNudgeSignature returns the persisted nudge-dedup JSON payload for a
// PR (empty string when the PR has no row or no signatures yet). The payload is
// opaque to storage; lifecycle.Manager owns its shape.
func (s *Store) GetPRLastNudgeSignature(ctx context.Context, url string) (string, error) {
	sig, err := s.qr.GetPRLastNudgeSignature(ctx, url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get pr nudge signature %s: %w", url, err)
	}
	return sig, nil
}

// UpdatePRLastNudgeSignature overwrites the persisted nudge-dedup JSON payload
// for a PR. A no-op when the URL has no pr row yet.
func (s *Store) UpdatePRLastNudgeSignature(ctx context.Context, url, payload string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpdatePRLastNudgeSignature(ctx, gen.UpdatePRLastNudgeSignatureParams{LastNudgeSignature: payload, URL: url}); err != nil {
		return fmt.Errorf("update pr nudge signature %s: %w", url, err)
	}
	return nil
}

// GetPR returns the PR facts for a URL, or ok=false if absent.
func (s *Store) GetPR(ctx context.Context, url string) (domain.PullRequest, bool, error) {
	p, err := s.qr.GetPRByURLOrAlias(ctx, url)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PullRequest{}, false, nil
	}
	if err != nil {
		return domain.PullRequest{}, false, fmt.Errorf("get pr %s: %w", url, err)
	}
	return prRowFromGen(p), true, nil
}

// GetPRByNumber returns the best matching tracked PR for the /prs/{id} path.
// Active rows are preferred over terminal rows, then the newest observation
// wins when the same provider number appears in more than one repository.
func (s *Store) GetPRByNumber(ctx context.Context, number int) (domain.PullRequest, bool, error) {
	if number <= 0 {
		return domain.PullRequest{}, false, nil
	}
	p, err := s.qr.GetPRByNumber(ctx, int64(number))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PullRequest{}, false, nil
	}
	if err != nil {
		return domain.PullRequest{}, false, fmt.Errorf("get pr by number %d: %w", number, err)
	}
	return prRowFromGen(p), true, nil
}

// CountActivePRsByNumber reports whether a numeric resolve path is ambiguous
// across simultaneously tracked repositories.
func (s *Store) CountActivePRsByNumber(ctx context.Context, number int) (int, error) {
	if number <= 0 {
		return 0, nil
	}
	count, err := s.qr.CountActivePRsByNumber(ctx, int64(number))
	if err != nil {
		return 0, fmt.Errorf("count active prs by number %d: %w", number, err)
	}
	return int(count), nil
}

// ListPRsBySession returns every PR owned by a session, newest first.
func (s *Store) ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error) {
	rows, err := s.qr.ListPRsBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list prs for %s: %w", sessionID, err)
	}
	out := make([]domain.PullRequest, 0, len(rows))
	for _, p := range rows {
		out = append(out, prRowFromGen(p))
	}
	return out, nil
}

// ListChecks returns every recorded check run for a PR.
func (s *Store) ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	rows, err := s.qr.ListChecksByPR(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list checks %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestCheck, 0, len(rows))
	for _, c := range rows {
		out = append(out, checkRowFromGen(c))
	}
	return out, nil
}

// ListPRComments returns a PR's review comments, oldest first.
func (s *Store) ListPRComments(ctx context.Context, prURL string) ([]domain.PullRequestComment, error) {
	rows, err := s.qr.ListPRComments(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr comments %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestComment, 0, len(rows))
	for _, c := range rows {
		out = append(out, commentFromGen(c))
	}
	return out, nil
}

// MarkPRCommentResolved records a provider-resolved review comment locally.
func (s *Store) MarkPRCommentResolved(ctx context.Context, prURL, commentID string) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	affected, err := s.qw.MarkPRCommentResolved(ctx, gen.MarkPRCommentResolvedParams{PRURL: prURL, CommentID: commentID})
	if err != nil {
		return false, fmt.Errorf("mark pr comment resolved %s/%s: %w", prURL, commentID, err)
	}
	return affected > 0, nil
}

// MarkPRReviewThreadResolved records a provider-resolved review thread and
// all of its comments locally. The two updates share the writer lock so a
// concurrent observation cannot interleave with the targeted state change.
func (s *Store) MarkPRReviewThreadResolved(ctx context.Context, prURL, threadID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "mark pr review thread resolved", func(q *gen.Queries) error {
		if _, err := q.MarkPRReviewThreadResolved(ctx, gen.MarkPRReviewThreadResolvedParams{PRURL: prURL, ThreadID: threadID}); err != nil {
			return fmt.Errorf("mark pr review thread resolved %s/%s: %w", prURL, threadID, err)
		}
		if _, err := q.MarkPRCommentsResolvedForThread(ctx, gen.MarkPRCommentsResolvedForThreadParams{PRURL: prURL, ThreadID: threadID}); err != nil {
			return fmt.Errorf("mark pr review comments resolved %s/%s: %w", prURL, threadID, err)
		}
		return nil
	})
}

// ListPRReviewThreads returns a PR's review threads, oldest first.
func (s *Store) ListPRReviewThreads(ctx context.Context, prURL string) ([]domain.PullRequestReviewThread, error) {
	rows, err := s.qr.ListPRReviewThreads(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr review threads %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestReviewThread, 0, len(rows))
	for _, th := range rows {
		out = append(out, reviewThreadFromGen(th))
	}
	return out, nil
}

// ListPRReviews returns a PR's submitted reviews, oldest first.
func (s *Store) ListPRReviews(ctx context.Context, prURL string) ([]domain.PullRequestReview, error) {
	rows, err := s.qr.ListPRReviews(ctx, prURL)
	if err != nil {
		return nil, fmt.Errorf("list pr reviews %s: %w", prURL, err)
	}
	out := make([]domain.PullRequestReview, 0, len(rows))
	for _, review := range rows {
		out = append(out, reviewFromGen(review))
	}
	return out, nil
}
