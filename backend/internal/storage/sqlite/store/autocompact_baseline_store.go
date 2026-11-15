package store

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// Baseline implements ports.AutoCompactBaselineReader: the median first-call
// input tokens of the newest domain.AutoCompactBaselineSessions sessions of the
// project, kind (planners and reviewers count as workers) and harness, or
// domain.AutoCompactBaselineDefault with too few of them.
func (s *Store) Baseline(ctx context.Context, projectID domain.ProjectID, kind domain.SessionKind, harness domain.AgentHarness) (int64, error) {
	samples, err := s.qr.ListAutoCompactBaselineSamples(ctx, gen.ListAutoCompactBaselineSamplesParams{
		ProjectID:   &projectID,
		Kind:        domain.AutoCompactBaselineKind(kind),
		Harness:     harness,
		SampleLimit: domain.AutoCompactBaselineSessions,
	})
	if err != nil {
		return 0, fmt.Errorf("read auto-compact baseline of %s %s %s: %w", projectID, kind, harness, err)
	}
	return domain.AutoCompactBaseline(samples), nil
}
