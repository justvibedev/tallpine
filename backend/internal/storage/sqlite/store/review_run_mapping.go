package store

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func reviewRunFromRow(r gen.ReviewRun) domain.ReviewRun {
	var deliveredAt *time.Time
	if r.DeliveredAt.Valid {
		t := r.DeliveredAt.Time
		deliveredAt = &t
	}
	run := domain.ReviewRun{
		ID:               r.ID,
		ReviewID:         r.ReviewID,
		SessionID:        r.SessionID,
		BatchID:          r.BatchID,
		Harness:          r.Harness,
		Model:            r.Model,
		ModelReason:      r.ModelReason,
		TriggerSource:    r.TriggerSource,
		PRURL:            r.PRURL,
		TargetSHA:        r.TargetSha,
		Status:           r.Status,
		Verdict:          r.Verdict,
		Body:             r.Body,
		GithubReviewID:   r.GithubReviewID,
		CreatedAt:        r.CreatedAt,
		DeliveredAt:      deliveredAt,
		AutoInjectReview: r.AutoInjectReview,
	}
	run.PostFailed = run.NotPostedToPR()
	return run
}
