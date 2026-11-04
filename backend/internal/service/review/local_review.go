package review

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
)

// sessionPRs lists a worker's tracked PRs, or its synthesized local review
// target when it tracks none.
func (s *Service) sessionPRs(ctx context.Context, worker domain.SessionRecord) ([]domain.PullRequest, error) {
	prs, err := s.store.ListPRsBySession(ctx, worker.ID)
	if err != nil {
		return nil, err
	}
	return reviewcore.WithLocalTarget(ctx, worker, prs)
}

func (s *Service) checkExpectedHead(ctx context.Context, workerID domain.SessionID, prURL, head string) (string, error) {
	worker, ok, err := s.store.GetSession(ctx, workerID)
	if err != nil {
		return "", err
	}
	if !ok {
		worker = domain.SessionRecord{ID: workerID}
	}
	tracked, err := s.sessionPRs(ctx, worker)
	if err != nil {
		return "", err
	}
	return reviewcore.CheckExpectedHead(tracked, prURL, head)
}
