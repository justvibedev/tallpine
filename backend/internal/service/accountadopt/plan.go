package accountadopt

import (
	"context"
	"sort"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Candidate is a live session that is not on the target account.
type Candidate struct {
	SessionID      domain.SessionID
	ProjectID      domain.ProjectID
	Title          string
	Kind           domain.SessionKind
	Activity       string
	AccountID      string
	IsOrchestrator bool
	Running        bool
}

// candidates lists the live, bound sessions of the target's provider that use
// another account: workers first (oldest first), orchestrators last.
func candidates(ctx context.Context, sessions Sessions, accounts Accounts, target domain.AgentAccount) ([]Candidate, error) {
	recs, err := sessions.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	type dated struct {
		c Candidate
		t int64
	}
	var picked []dated
	for _, rec := range recs {
		if rec.IsTerminated {
			continue
		}
		if p, ok := domain.AgentAccountProviderFor(rec.Harness); !ok || p != target.Provider {
			continue
		}
		account, bound, err := accounts.SessionAccount(ctx, rec.ID, target.Provider)
		if err != nil {
			return nil, err
		}
		if !bound || account.ID == target.ID {
			continue
		}
		title := rec.DisplayName
		if title == "" {
			title = string(rec.ID)
		}
		picked = append(picked, dated{c: Candidate{
			SessionID: rec.ID, ProjectID: rec.ProjectID, Title: title, Kind: rec.Kind,
			Activity: string(rec.Activity.State), AccountID: account.ID,
			IsOrchestrator: rec.Kind == domain.KindOrchestrator,
			Running:        rec.Activity.State != domain.ActivityExited,
		}, t: rec.CreatedAt.UnixNano()})
	}
	sort.SliceStable(picked, func(i, j int) bool {
		if picked[i].c.IsOrchestrator != picked[j].c.IsOrchestrator {
			return !picked[i].c.IsOrchestrator
		}
		return picked[i].t < picked[j].t
	})
	out := make([]Candidate, len(picked))
	for i, p := range picked {
		out[i] = p.c
	}
	return out, nil
}
