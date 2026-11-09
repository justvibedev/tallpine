package pr

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// applyNoCIGate applies the no-CI review policy to a fresh observation: when
// the PR reports no CI and its project opted into NoCI, a Tallpine review
// approval on exactly head stands in for CI, and its absence is a blocker.
func (s *ActionService) applyNoCIGate(ctx context.Context, tracked domain.PullRequest, head string, fresh ports.SCMObservation) (ports.SCMObservation, error) {
	if !ports.NoCIGateApplies(fresh) {
		return fresh, nil
	}
	noCI, err := s.projectNoCI(ctx, tracked.SessionID)
	if err != nil || !noCI {
		return fresh, err
	}
	gate, err := s.noCIReviewGate(ctx, tracked, head)
	if err != nil {
		return fresh, err
	}
	if blocker := gate.Blocker(); blocker != domain.MergeBlockerNone {
		return fresh, &PreconditionError{Blocker: blocker}
	}
	return ports.ApplyNoCIPolicy(fresh, true, gate), nil
}

// projectNoCI reports whether the PR's owning project opted into
// ProjectConfig.NoCI. A missing session or project means no opt-in.
func (s *ActionService) projectNoCI(ctx context.Context, sessionID domain.SessionID) (bool, error) {
	if s.policy == nil || sessionID == "" {
		return false, nil
	}
	sess, ok, err := s.policy.GetSession(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("load pull request session: %w", err)
	}
	if !ok {
		return false, nil
	}
	project, ok, err := s.policy.GetProject(ctx, string(sess.ProjectID))
	if err != nil {
		return false, fmt.Errorf("load pull request project: %w", err)
	}
	return ok && project.Config.NoCI, nil
}

// noCIReviewGate judges the Tallpine review passes the PR's owning session recorded
// against exactly head. The provider review decision is not used: it is not
// tied to a commit (see domain.CIWithNoCIPolicy).
func (s *ActionService) noCIReviewGate(ctx context.Context, tracked domain.PullRequest, head string) (domain.TallpineReviewGate, error) {
	runs, err := s.policy.ListReviewRunsBySession(ctx, tracked.SessionID)
	if err != nil {
		return "", fmt.Errorf("load Tallpine review runs: %w", err)
	}
	return domain.TallpineReviewGateForHead(runs, tracked.URL, head), nil
}
