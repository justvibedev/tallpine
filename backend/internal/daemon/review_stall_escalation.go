package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// escalateStalledReview tells the orchestrator that a PR head has waited for a
// reviewer for a long time, so a stalled review cannot sit silently (B389).
// The report ID is stable per PR and head.
func (b *boot) escalateStalledReview(ctx context.Context, session domain.SessionRecord, prURL, headSHA string, waited time.Duration) {
	sum := sha256.Sum256([]byte(prURL))
	msg := fmt.Sprintf("[auto] review_stalled: %s has needed review for %d min with no reviewer scheduled. Check Auto review in project settings, or start one by hand (`%s review trigger %s`).",
		prURL, int(waited.Minutes()), brand.CLIName, session.ID)
	_, err := b.reportSvc.Create(ctx, reportsvc.CreateInput{
		ID:        fmt.Sprintf("review-stalled-%s-%s", hex.EncodeToString(sum[:6]), headSHA),
		SessionID: session.ID, State: domain.ReportNeedsInput, Note: msg, Synthesized: true,
	})
	if err != nil && !errors.Is(err, domain.ErrDuplicateReport) {
		b.log.Warn("review stall escalation failed", "session", session.ID, "pr", prURL, "error", err)
	}
}

// The store supplies the per-session override and app defaults the Auto review
// gates read; a store without them would silently lose an explicit disable.
var _ reviewcore.AutoReviewSource = (*sqlite.Store)(nil)
