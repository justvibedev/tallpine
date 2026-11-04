package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// escalateReviewRounds tells the orchestrator that a PR used up its automatic
// review rounds. The report ID is stable per PR and blocked head, so the sweep
// that re-evaluates the worker files it once per stuck head.
func (b *boot) escalateReviewRounds(ctx context.Context, session domain.SessionRecord, prURL, headSHA string, rounds int) {
	sum := sha256.Sum256([]byte(prURL))
	msg := fmt.Sprintf("[auto] review_rounds_exhausted: %s has had %d review passes that requested changes; automatic re-review is stopped for this PR. Decide whether to continue by hand (`%s review trigger %s`), take the work over, or close it.",
		prURL, rounds, brand.CLIName, session.ID)
	_, err := b.reportSvc.Create(ctx, reportsvc.CreateInput{
		ID:        fmt.Sprintf("review-rounds-%s-%s", hex.EncodeToString(sum[:6]), headSHA),
		SessionID: session.ID, State: domain.ReportNeedsInput, Note: msg, Synthesized: true,
	})
	if err != nil && !errors.Is(err, domain.ErrDuplicateReport) {
		b.log.Warn("review rounds escalation failed", "session", session.ID, "pr", prURL, "error", err)
	}
}
