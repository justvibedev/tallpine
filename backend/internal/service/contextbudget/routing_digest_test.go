package contextbudget

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	asksvc "tallpine.eu/tallpine/backend/internal/service/ask"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// digestStub reports a fixed set of findings.
type digestStub struct{ fs []domain.ContextHealthFinding }

func (d digestStub) Find(context.Context, domain.ProjectRecord, Measurement) ([]domain.ContextHealthFinding, error) {
	return d.fs, nil
}

func weeklyDigest() domain.ContextHealthFinding {
	return domain.ContextHealthFinding{Kind: domain.ContextRoutingPointer, Fingerprint: DigestFingerprint, MeasuredTokens: 2, BudgetTokens: routingBudget, Evidence: "2 doc-pointer gaps this week"}
}

func TestOldPerSearchCardIsWithdrawnAndOneWeeklyCardFiled(t *testing.T) {
	r := newRig(t, true)
	r.claude = "small"
	r.finder = []Finder{digestStub{[]domain.ContextHealthFinding{{Kind: domain.ContextRoutingPointer, Fingerprint: "routing_pointer:search:abc", MeasuredTokens: 3, BudgetTokens: routingBudget, Evidence: "search x"}}}}
	r.sweep(0)
	r.finder = []Finder{digestStub{[]domain.ContextHealthFinding{weeklyDigest()}}}
	r.sweep(25 * time.Hour)
	var withdrawn, open int
	for _, a := range r.asksList() {
		switch {
		case a.State == domain.AskWithdrawn && strings.Contains(a.WithdrawReason, "Replaced by the weekly"):
			withdrawn++
		case a.State == domain.AskOpen && strings.Contains(a.About, DigestFingerprint):
			open++
		}
	}
	if withdrawn != 1 || open != 1 {
		t.Fatalf("withdrawn=%d open=%d; asks = %+v", withdrawn, open, r.asksList())
	}
}

func TestWeekOfGapsBecomesOneDigestWithEvidenceAndOnePatch(t *testing.T) {
	files := docFiles()
	files["docs/runbook.md"] = "# Ops\n"
	rows := foundRows("docs/deploy.md", "s1", "s2", "s3")
	rows = append(rows, foundRows("docs/runbook.md", "s4", "s5", "s6")...)
	got := digestOf(files, rows...)
	if len(got) != 1 || got[0].Fingerprint != DigestFingerprint || got[0].Kind != domain.ContextRoutingPointer {
		t.Fatalf("findings = %+v, want exactly one weekly digest", got)
	}
	ev := got[0].Evidence
	for _, want := range []string{"2 doc-pointer gaps", "`docs/deploy.md`", "`docs/runbook.md`", "`runbook-s4`", "searched", "then read", "tool calls", "add this line to AGENTS.md", "pointing at `docs/runbook.md`"} {
		if !strings.Contains(ev, want) {
			t.Fatalf("evidence lacks %q:\n%s", want, ev)
		}
	}
	assertPatchApplies(t, "AGENTS.md", agentsMD, got[0].Patch, "- When working on Ops, read `docs/runbook.md` first.")
	if !strings.Contains(got[0].Patch, "read `docs/deploy.md` first.") {
		t.Fatalf("one patch must carry every doc line: %s", got[0].Patch)
	}
}

func TestNoGapsFilesNoDigest(t *testing.T) {
	if got := digestOf(docFiles(), read("s1", "docs/deploy.md"), read("s2", "docs/deploy.md")); len(got) != 0 {
		t.Fatalf("no gaps gave %+v", got)
	}
}

func gapRig(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, true)
	r.claude = "small"
	return r
}

func (r *rig) routingFrom(files map[string]string, rows []store.ToolActivity) {
	r.finder = []Finder{RoutingFinder{
		Store: fakeActivity{rows, 5}, ArtifactRoot: r.root, Now: func() time.Time { return r.now },
		Reader: func(context.Context, domain.ProjectRecord) (Source, error) { return memSource(files), nil },
	}}
}

func TestNoOnTheDigestDropsItsGapsAndTheNextCardShowsOnlyTheNewOne(t *testing.T) {
	files := docFiles()
	files["CLAUDE.md"] = "small"
	files["docs/runbook.md"] = "# Runbook\n"
	files["docs/reference.md"] = "# Reference\n"
	r := gapRig(t)
	rows := append(foundRows("docs/deploy.md", "s1", "s2", "s3"), foundRows("docs/runbook.md", "s4", "s5", "s6")...)
	r.routingFrom(files, rows)
	r.sweep(0)
	r.answer("Q1", "no", "")
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 1 {
		t.Fatalf("asks = %d, want the declined gaps to stay quiet", got)
	}
	r.routingFrom(files, append(rows, foundRows("docs/reference.md", "s7", "s8", "s9")...))
	r.sweep(25 * time.Hour)
	asks := r.asksList()
	if len(asks) != 2 {
		t.Fatalf("asks = %d, want a card for the new gap", len(asks))
	}
	f := r.findings()[DigestFingerprint]
	if !strings.Contains(f.Evidence, "docs/reference.md") || strings.Contains(f.Evidence, "docs/deploy.md") || strings.Contains(f.Evidence, "docs/runbook.md") {
		t.Fatalf("evidence must show only the new gap:\n%s", f.Evidence)
	}
}

func TestGapsChangingWhileTheCardIsOpenKeepsTheSameCard(t *testing.T) {
	r := newRig(t, true)
	r.claude = "small"
	r.finder = []Finder{digestStub{[]domain.ContextHealthFinding{weeklyDigest()}}}
	r.sweep(0)
	d := weeklyDigest()
	d.Evidence = "3 doc-pointer gaps this week"
	r.finder = []Finder{digestStub{[]domain.ContextHealthFinding{d}}}
	r.sweep(25 * time.Hour)
	asks := r.asksList()
	if len(asks) != 1 || asks[0].State != domain.AskOpen {
		t.Fatalf("asks = %+v, want the one card still open", asks)
	}
	if f := r.findings()[DigestFingerprint]; f.Evidence != d.Evidence {
		t.Fatalf("evidence = %q, want it refreshed in place", f.Evidence)
	}
}

func TestWithdrawnDigestCardIsReadAsNoAndNotRefiled(t *testing.T) {
	files := docFiles()
	files["CLAUDE.md"] = "small"
	r := gapRig(t)
	rows := foundRows("docs/deploy.md", "s1", "s2", "s3")
	r.routingFrom(files, rows)
	r.sweep(0)
	if _, err := r.asks.Withdraw(context.Background(), proj, asksvc.Actor{Kind: domain.BacklogOriginHuman}, "Q1", "not now"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		r.sweep(25 * time.Hour)
		if got := len(r.asksList()); got != 1 {
			t.Fatalf("asks = %d, want the withdrawn gaps not refiled", got)
		}
	}
}

func TestGapThatAppearsWhileTheCardIsOpenIsNotDeclinedByTheNo(t *testing.T) {
	files := docFiles()
	files["CLAUDE.md"] = "small"
	files["docs/reference.md"] = "# Reference\n"
	r := gapRig(t)
	rows := foundRows("docs/deploy.md", "s1", "s2", "s3")
	r.routingFrom(files, rows)
	r.sweep(0) // card filed for deploy.md only
	r.routingFrom(files, append(rows, foundRows("docs/reference.md", "s4", "s5", "s6")...))
	r.sweep(25 * time.Hour) // a gap appears; the owner has not seen it
	r.answer("Q1", "no", "")
	r.sweep(25 * time.Hour)
	if got := len(r.asksList()); got != 2 {
		t.Fatalf("asks = %d, want a new card for the gap the No never covered", got)
	}
	if f := r.findings()[DigestFingerprint]; !strings.Contains(f.Evidence, "docs/reference.md") || strings.Contains(f.Evidence, "docs/deploy.md") {
		t.Fatalf("evidence must show only the unseen gap:\n%s", f.Evidence)
	}
}

func TestRefiledDigestDoesNotInheritTheDeclinedCardsAnalystDraft(t *testing.T) {
	r := newRig(t, true)
	r.claude = "small"
	r.finder = []Finder{digestStub{[]domain.ContextHealthFinding{weeklyDigest()}}}
	r.sweep(0)
	dir := r.findings()[DigestFingerprint].ArtifactDir
	for _, n := range []string{AnalystNotesFile, AnalystPatchFile} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("old gaps"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.answer("Q1", "no", "")
	d := weeklyDigest()
	d.Evidence = "1 new doc-pointer gap"
	r.finder = []Finder{digestStub{[]domain.ContextHealthFinding{d}}}
	r.sweep(25 * time.Hour)
	f := r.findings()[DigestFingerprint]
	if f.State != domain.ContextFindingOpen || len(r.asksList()) != 2 {
		t.Fatalf("state %s, asks %d: want a refiled card", f.State, len(r.asksList()))
	}
	for _, n := range []string{AnalystNotesFile, AnalystPatchFile} {
		if _, err := os.Stat(filepath.Join(f.ArtifactDir, n)); err == nil {
			t.Fatalf("%s of the declined card survived", n)
		}
	}
	if _, brief := Brief(f); strings.Contains(brief, AnalystPatchFile) {
		t.Fatalf("brief points at a stale draft: %s", brief)
	}
}
