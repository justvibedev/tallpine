package session

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestClaimPRAcceptsEveryAdvisedBranchForm covers each head-branch form the
// worker prompt recommends (B456), through the options the explicit claim-pr
// route and a report claim pass.
func TestClaimPRAcceptsEveryAdvisedBranchForm(t *testing.T) {
	t.Parallel()

	forms := []struct {
		name, branch, head string
		kind               domain.ProjectKind
	}{
		{"root branch itself", "tp/demo-1/root", "tp/demo-1/root", domain.ProjectKindSingleRepo},
		{"root sibling", "tp/demo-1/root", "tp/demo-1/fix", domain.ProjectKindSingleRepo},
		{"root stacked sibling", "tp/demo-1/root", "tp/demo-1/fix-more", domain.ProjectKindSingleRepo},
		{"workspace hyphen sibling", "tp/demo-1", "tp/demo-1-fix", domain.ProjectKindWorkspace},
		{"workspace collision sibling", "tp/demo-1-2", "tp/demo-1-2-fix", domain.ProjectKindWorkspace},
		{"workspace stacked sibling", "tp/demo-1", "tp/demo-1-fix-more", domain.ProjectKindWorkspace},
		{"custom branch itself", "feature/a", "feature/a", domain.ProjectKindSingleRepo},
		{"custom additional PR", "feature/a", "feature/a-fix", domain.ProjectKindSingleRepo},
		{"custom stacked PR", "feature/a", "feature/a-fix-more", domain.ProjectKindSingleRepo},
		{"workspace custom additional PR", "feature/a", "feature/a-fix", domain.ProjectKindWorkspace},
	}
	routes := []struct {
		name string
		opts ClaimPROptions
	}{
		{"claim-pr", ClaimPROptions{AllowTakeover: true, RecordInteraction: true}},
		{"report", ClaimPROptions{RefuseOtherSessionBranch: true}},
	}
	for _, route := range routes {
		for _, form := range forms {
			t.Run(route.name+"/"+form.name, func(t *testing.T) {
				t.Parallel()
				st := newFakeStore()
				st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws", Branch: form.branch}}
				st.sessions["demo-2"] = domain.SessionRecord{ID: "demo-2", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws2", Branch: "tp/demo-2/root"}}
				st.projects["demo"] = domain.ProjectRecord{ID: "demo", Kind: form.kind, RepoOriginURL: "https://github.com/acme/repo"}
				claimer := &fakePRClaimer{}
				scm := fakeSCM{obs: ports.SCMObservation{
					Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
					PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, SourceBranch: form.head},
				}}
				svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: claimer})
				if _, err := svc.ClaimPR(context.Background(), "demo-1", "7", route.opts); err != nil || !claimer.called {
					t.Fatalf("head %q on session branch %q: err=%v called=%v, want claimed", form.head, form.branch, err, claimer.called)
				}
			})
		}
	}
}
