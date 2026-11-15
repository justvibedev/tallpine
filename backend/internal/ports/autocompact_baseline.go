package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// AutoCompactBaselineReader measures the context a session starts with
// (domain.AutoCompactBaseline): the median first-call input tokens of the
// newest sessions of the same project, baseline kind
// (domain.AutoCompactBaselineKind) and harness. Read at launch only.
type AutoCompactBaselineReader interface {
	Baseline(ctx context.Context, projectID domain.ProjectID, kind domain.SessionKind, harness domain.AgentHarness) (int64, error)
}
