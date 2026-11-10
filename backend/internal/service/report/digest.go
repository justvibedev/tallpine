package report

import (
	"context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// QuietFlusher is an optional Store capability behind the digest threshold.
type QuietFlusher interface {
	FlushQuietReports(ctx context.Context, projectID domain.ProjectID, minCount int, at time.Time) (int, error)
}

// flushDigest releases the project's waiting quiet reports once the configured
// count is queued. Best effort: on failure they keep their time-based delay.
func (s *Service) flushDigest(ctx context.Context, projectID domain.ProjectID) {
	f, ok := s.store.(QuietFlusher)
	if !ok || s.digestCount == nil {
		return
	}
	n := s.digestCount(ctx, projectID)
	if n <= 0 {
		return
	}
	if _, err := f.FlushQuietReports(ctx, projectID, n, s.now().UTC()); err != nil {
		slog.Default().Warn("report: digest flush failed", "project", projectID, "err", err)
	}
}
