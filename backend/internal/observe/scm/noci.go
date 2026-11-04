package scm

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// projectNoCILookup returns a per-poll memoized reader of
// ProjectConfig.NoCI. A failed project read is treated as opted out, which
// keeps today's conservative readiness rule.
func (o *Observer) projectNoCILookup() func(context.Context, domain.ProjectID) bool {
	cache := map[domain.ProjectID]bool{}
	return func(ctx context.Context, id domain.ProjectID) bool {
		if v, ok := cache[id]; ok {
			return v
		}
		p, found, err := o.store.GetProject(ctx, string(id))
		if err != nil {
			o.logger.Warn("scm observer: read project config for noCI failed", "project", id, "err", err)
		}
		v := err == nil && found && p.Config.NoCI
		cache[id] = v
		return v
	}
}

// noCIStateStale reports whether s's stored CI state no longer matches the
// project's noCI review gate, which can change without any provider-side
// change the ETag guards would notice: the head got approved after Tallpine stored
// "unknown", or a waived "passing" (domain.PullRequest.NoCIWaived) lost
// its opt-in or its approval. Such a PR is refetched so the observer re-applies
// the gate; each stored state forces at most one successful refetch. It
// returns the staleness token the caller records once that refetch succeeds.
func (o *Observer) noCIStateStale(ctx context.Context, key string, s *subject, noCI func(context.Context, domain.ProjectID) bool) (string, bool) {
	local := s.known
	if local.Merged || local.Closed || local.HeadSHA == "" {
		delete(o.noCIRecheck, key)
		return "", false
	}
	optedIn := noCI(ctx, s.session.ProjectID)
	var stale bool
	gate := domain.TallpineReviewGateMissing
	switch local.CI {
	case "", domain.CIUnknown:
		if !optedIn {
			// A PR the gate held back as blocked gets its provider
			// mergeability back once the project opts out.
			stale = local.NoCIAwaitingApproval()
			break
		}
		gate = o.noCIReviewGate(ctx, s.session.ID, local.URL, local.HeadSHA)
		// Approval arrived after "unknown" was stored, or noCI was turned on
		// for a PR stored before the gate ever ran on it (no hold marker), so
		// a provider "mergeable" still reads Mergeable/Ready until refetched.
		stale = gate == domain.TallpineReviewGateApproved || !local.NoCIAwaitingApproval()
	case domain.CIPassing:
		if !local.NoCIWaived() {
			delete(o.noCIRecheck, key)
			return "", false
		}
		if optedIn {
			gate = o.noCIReviewGate(ctx, s.session.ID, local.URL, local.HeadSHA)
		}
		stale = !optedIn || gate != domain.TallpineReviewGateApproved
	}
	if !stale {
		delete(o.noCIRecheck, key)
		return "", false
	}
	token := fmt.Sprintf("%s|%s|%t", local.CIHash, gate, optedIn)
	if o.noCIRecheck[key] == token {
		return "", false
	}
	return token, true
}

// noCIReviewGate judges the Tallpine review passes sessionID recorded against
// exactly head for the noCI gate. A failed read is treated as not approved.
func (o *Observer) noCIReviewGate(ctx context.Context, sessionID domain.SessionID, prURL, head string) domain.TallpineReviewGate {
	if o.reviewRuns == nil {
		return domain.TallpineReviewGateMissing
	}
	runs, err := o.reviewRuns.ListReviewRunsBySession(ctx, sessionID)
	if err != nil {
		o.logger.Warn("scm observer: read Tallpine review runs for noCI failed", "session", sessionID, "err", err)
		return domain.TallpineReviewGateMissing
	}
	return domain.TallpineReviewGateForHead(runs, prURL, head)
}
