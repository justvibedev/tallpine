package daemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// openPRSCM reports every requested PR as open in acme/repo.
type openPRSCM struct{}

func (openPRSCM) ParseRepository(string) (ports.SCMRepo, bool) { return ports.SCMRepo{}, false }

func (openPRSCM) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	out := make([]ports.SCMObservation, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ports.SCMObservation{
			Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
			PR: ports.SCMPRObservation{URL: fmt.Sprintf("https://github.com/acme/repo/pull/%d", ref.Number), Number: ref.Number},
		})
	}
	return out, nil
}

func (openPRSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return ports.SCMReviewObservation{}, nil
}

// A report links only a PR nobody else owns. A worker cannot move a PR to
// itself by reporting it, not even from a terminated sibling or from a
// terminated session of another project on the same repository.
func TestReportedPRClaimNeverTakesAnotherSessionsPR(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for _, id := range []domain.ProjectID{"mer", "web"} {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: string(id), Path: "/tmp/" + string(id), RepoOriginURL: "https://github.com/acme/repo", RegisteredAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	newWorker := func(project domain.ProjectID, terminated bool) domain.SessionRecord {
		t.Helper()
		rec, err := store.CreateSession(ctx, domain.SessionRecord{
			ProjectID: project, Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
			Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: t.TempDir()},
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	terminate := func(rec domain.SessionRecord) {
		t.Helper()
		rec.IsTerminated = true
		rec.UpdatedAt = time.Now().UTC().Truncate(time.Second)
		if err := store.UpdateSession(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	own := func(owner domain.SessionRecord, number int) string {
		t.Helper()
		url := fmt.Sprintf("https://github.com/acme/repo/pull/%d", number)
		if _, err := store.ClaimPR(ctx, domain.PullRequest{URL: url, SessionID: owner.ID, Number: number, UpdatedAt: now}, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
			t.Fatal(err)
		}
		return url
	}

	sibling := newWorker("mer", false)
	otherProject := newWorker("web", false)
	reporter := newWorker("mer", false)
	siblingPR := own(sibling, 7)
	otherProjectPR := own(otherProject, 8)
	terminate(sibling)
	terminate(otherProject)
	unownedPR := "https://github.com/acme/repo/pull/9"

	sessions := sessionsvc.NewWithDeps(sessionsvc.Deps{Store: store, PRClaimer: store, SCM: openPRSCM{}})
	reports := reportsvc.New(reportsvc.Deps{Store: store, ClaimReportedPR: reportedPRClaimer{sessions: sessions}.Claim})
	rep, err := reports.Create(ctx, reportsvc.CreateInput{
		SessionID: reporter.ID, State: domain.ReportDone, Note: "opened PRs",
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputPRCreated, Reference: siblingPR},
			{Kind: domain.ReportOutputPRCreated, Reference: otherProjectPR},
			{Kind: domain.ReportOutputPRCreated, Reference: unownedPR},
		},
	})
	if err != nil {
		t.Fatalf("refused claims must not fail the report: %v", err)
	}
	// B324: PRs the reporter could not link are not presented as its own.
	if len(rep.Outputs) != 1 || rep.Outputs[0].Reference != unownedPR {
		t.Fatalf("stored outputs = %+v, want only the linked PR", rep.Outputs)
	}

	for url, owner := range map[string]domain.SessionID{siblingPR: sibling.ID, otherProjectPR: otherProject.ID, unownedPR: reporter.ID} {
		got, ok, err := store.GetPR(ctx, url)
		if err != nil || !ok {
			t.Fatalf("GetPR(%s) = %v, %v", url, ok, err)
		}
		if got.SessionID != owner {
			t.Fatalf("%s owned by %s, want %s", url, got.SessionID, owner)
		}
	}
}
