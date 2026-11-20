package contextbudget

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type fakeActivity struct {
	rows   []store.ToolActivity
	active int
}

func (f fakeActivity) CountActiveSessions(context.Context, domain.ProjectID, time.Time) (int, error) {
	return f.active, nil
}

func (f fakeActivity) ListToolActivity(context.Context, domain.ProjectID, time.Time) ([]store.ToolActivity, error) {
	return f.rows, nil
}

func memSource(files map[string]string) Source {
	return Source{Read: func(_ context.Context, p string) ([]byte, bool, error) {
		c, ok := files[p]
		return []byte(c), ok, nil
	}}
}

const agentsMD = "# Agents\n\nRules.\n- Run tests.\n"

func routing(files map[string]string, rows ...store.ToolActivity) ([]gap, error) {
	r := RoutingFinder{
		Store:  fakeActivity{rows, 5},
		Reader: func(context.Context, domain.ProjectRecord) (Source, error) { return memSource(files), nil },
	}
	m := Measurement{Groups: []group{{Report: Report{Files: []File{{Path: "AGENTS.md"}}}}}}
	gs, _, err := r.gaps(context.Background(), domain.ProjectRecord{ID: proj, Path: "/repo"}, m)
	return gs, err
}

func digestOf(files map[string]string, rows ...store.ToolActivity) []domain.ContextHealthFinding {
	r := RoutingFinder{
		Store:  fakeActivity{rows, 5},
		Reader: func(context.Context, domain.ProjectRecord) (Source, error) { return memSource(files), nil },
	}
	m := Measurement{Groups: []group{{Report: Report{Files: []File{{Path: "AGENTS.md"}}}}}}
	out, _ := r.Find(context.Background(), domain.ProjectRecord{ID: proj, Path: "/repo"}, m)
	return out
}

func read(session, p string) store.ToolActivity {
	return store.ToolActivity{SessionID: domain.SessionID(session), Kind: domain.ActivityKindMCPTool, ToolKind: "read", Locations: `[{"path":"` + p + `"}]`}
}

// found is a read that follows a search, the only kind of read a pointer helps.
// Rows come newest first, as the store returns them.
func found(session, p string) []store.ToolActivity {
	name := strings.TrimSuffix(path.Base(p), path.Ext(p))
	return []store.ToolActivity{read(session, p), cmd(session, "grep -rn "+name+"-"+session+" .")}
}

func foundRows(p string, sessions ...string) []store.ToolActivity {
	var rows []store.ToolActivity
	for _, s := range sessions {
		rows = append(rows, found(s, p)...)
	}
	return rows
}

func cmd(session, c string) store.ToolActivity {
	return store.ToolActivity{SessionID: domain.SessionID(session), Kind: domain.ActivityKindCommand, Command: c}
}

// thenRead appends, for each session, a later read of a doc, which is what makes
// that session's earlier searches count. Rows are newest first.
func thenRead(rows []store.ToolActivity, sessions ...string) []store.ToolActivity {
	var out []store.ToolActivity
	for _, s := range sessions {
		out = append(out, read(s, "docs/deploy.md"))
	}
	return append(out, rows...)
}

// huntRead is thenRead for searches: each session also took a step between the
// search and the doc, which is what a pointer would have saved.
func huntRead(rows []store.ToolActivity, sessions ...string) []store.ToolActivity {
	var out []store.ToolActivity
	for _, s := range sessions {
		out = append(out, read(s, "docs/deploy.md"), cmd(s, "ls docs"))
	}
	return append(out, rows...)
}

var fiveSessions = []string{"s1", "s2", "s3", "s4", "s5"}

func docFiles() map[string]string {
	return map[string]string{"AGENTS.md": agentsMD, "docs/deploy.md": "# Deploying\n\nsteps\n"}
}

func TestUnlinkedDocReadInThreeSessionsGetsAPointerPatch(t *testing.T) {
	got, err := routing(docFiles(), foundRows("docs/deploy.md", "s1", "s2", "s3")...)
	if err != nil || len(got) != 1 {
		t.Fatalf("findings = %+v, %v; want one", got, err)
	}
	f := got[0]
	if f.Kind != domain.ContextRoutingPointer || f.Fingerprint != "routing_pointer:doc:docs/deploy.md" || f.MeasuredTokens != 3 {
		t.Fatalf("finding = %+v", f)
	}
	if !strings.Contains(f.Patch, "+- When working on Deploying, read `docs/deploy.md` first.") || !strings.Contains(f.Patch, "--- a/AGENTS.md") {
		t.Fatalf("patch = %q, want the pointer line added to AGENTS.md", f.Patch)
	}
	if !strings.Contains(f.Evidence, "no instruction file names") {
		t.Fatalf("evidence = %q", f.Evidence)
	}
	assertPatchApplies(t, "AGENTS.md", agentsMD, f.Patch, "- When working on Deploying, read `docs/deploy.md` first.")
}

func assertPatchApplies(t *testing.T, name, content, patch, want string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	pf := filepath.Join(t.TempDir(), "p.patch")
	if err := os.WriteFile(pf, []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "apply", pf).CombinedOutput(); err != nil {
		t.Fatalf("git apply: %v\n%s\npatch:\n%s", err, out, patch)
	}
	data, _ := os.ReadFile(filepath.Join(dir, name))
	if !strings.Contains(string(data), want) {
		t.Fatalf("patched file = %q, want %q", data, want)
	}
}

func TestDocReadInTwoSessionsOrOneSessionRepeatedGivesNothing(t *testing.T) {
	got, _ := routing(docFiles(), read("s1", "docs/deploy.md"), read("s2", "docs/deploy.md"))
	if len(got) != 0 {
		t.Fatalf("two sessions gave %+v", got)
	}
	got, _ = routing(docFiles(), read("s1", "docs/deploy.md"), read("s1", "docs/deploy.md"), read("s1", "docs/deploy.md"), read("s2", "docs/deploy.md"))
	if len(got) != 0 {
		t.Fatalf("one busy session counted more than once: %+v", got)
	}
}

func TestDocNamedInInstructionFilesGetsNoCard(t *testing.T) {
	files := docFiles()
	files["AGENTS.md"] = "# Agents\n\n- Deployment notes live in docs/deploy.md somewhere.\n"
	if got, _ := routing(files, foundRows("docs/deploy.md", "s1", "s2", "s3")...); len(got) != 0 {
		t.Fatalf("a doc AGENTS.md names gave %+v", got)
	}
}

func TestDirectPathReadsGetNoCard(t *testing.T) {
	files := docFiles()
	files["CHANGELOG.md"], files["README.md"] = "# Changes\n", "# Readme\n"
	var rows []store.ToolActivity
	for _, s := range []string{"s1", "s2", "s3"} {
		rows = append(rows, read(s, "docs/deploy.md"))
	}
	if got, _ := routing(files, rows...); len(got) != 0 {
		t.Fatalf("reads by direct path gave %+v", got)
	}
	// Reads that follow a search from long before do not count either.
	old := []store.ToolActivity{}
	for _, s := range []string{"s1", "s2", "s3"} {
		old = append(old, read(s, "docs/deploy.md"))
		for range searchLookback + 1 {
			old = append(old, cmd(s, "go test ./..."))
		}
		old = append(old, cmd(s, "rg deploy-"+s+" ."))
	}
	if got, _ := routing(files, old...); len(got) != 0 {
		t.Fatalf("a stale search made the read count: %+v", got)
	}
}

func TestUnrelatedSearchBeforeDirectReadGetsNoCard(t *testing.T) {
	var rows []store.ToolActivity
	for _, s := range []string{"s1", "s2", "s3"} {
		rows = append(rows, read(s, "docs/deploy.md"), cmd(s, "rg someFunc-"+s+" backend/"))
	}
	if got, _ := routing(docFiles(), rows...); len(got) != 0 {
		t.Fatalf("an unrelated search made the direct read count: %+v", got)
	}
}

func TestSearchOptionsAndPathsDoNotNameTheDoc(t *testing.T) {
	files := docFiles()
	files["docs/content.md"] = "# Content\n"
	var rows []store.ToolActivity
	for _, s := range []string{"s1", "s2", "s3"} {
		rows = append(rows, read(s, "docs/content.md"),
			store.ToolActivity{SessionID: domain.SessionID(s), Kind: domain.ActivityKindMCPTool, ToolKind: "search", ProviderToolName: "Grep",
				Input: `{"pattern":"foo-` + s + `","output_mode":"content","path":"/w/content/docs"}`},
			cmd(s, "rg -n foo-"+s+" content/"))
	}
	if got, _ := routing(files, rows...); len(got) != 0 {
		t.Fatalf("option values and paths made the read count: %+v", got)
	}
}

func TestSeveralWrongDocsFirstCounts(t *testing.T) {
	files := docFiles()
	files["docs/a.md"], files["docs/b.md"] = "# A\n", "# B\n"
	var rows []store.ToolActivity // newest first
	for _, s := range []string{"s1", "s2", "s3"} {
		rows = append(rows, read(s, "docs/deploy.md"), read(s, "docs/b.md"), read(s, "docs/a.md"))
	}
	got, _ := routing(files, rows...)
	if len(got) != 1 || got[0].Fingerprint != "routing_pointer:doc:docs/deploy.md" {
		t.Fatalf("findings = %+v, want the doc found after wrong ones", got)
	}
}

func TestChangelogAndReadmeAfterASearchGetNoCard(t *testing.T) {
	files := docFiles()
	files["CHANGELOG.md"] = "# Changes\n"
	if got, _ := routing(files, foundRows("CHANGELOG.md", "s1", "s2", "s3")...); len(got) != 0 {
		t.Fatalf("CHANGELOG gave %+v", got)
	}
}

func TestCardNamesLineFileAndTokensSaved(t *testing.T) {
	got, _ := routing(docFiles(), foundRows("docs/deploy.md", "s1", "s2", "s3")...)
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	for _, want := range []string{"add this line to AGENTS.md", "- When working on Deploying, read `docs/deploy.md` first.", "tokens per session", "6000 tokens"} {
		if !strings.Contains(got[0].Evidence, want) {
			t.Fatalf("evidence lacks %q: %s", want, got[0].Evidence)
		}
	}
}

func TestIdenticalSearchInFiveSessionsIsAFinding(t *testing.T) {
	rows := []store.ToolActivity{
		{SessionID: "s1", Kind: domain.ActivityKindMCPTool, ToolKind: "search", Input: `{"pattern":"retryBackoff","output_mode":"content"}`},
		cmd("s2", "rg -n -g '*.go' retryBackoff backend/"),
		cmd("s3", "cd x && grep -rn 'retryBackoff' ."),
		cmd("s4", "grep -rn retryBackoff ."),
		cmd("s5", "rg retryBackoff"),
	}
	rows = huntRead(rows, fiveSessions...)
	got, err := routing(docFiles(), rows...)
	if err != nil || len(got) != 1 || !strings.HasPrefix(got[0].Fingerprint, "routing_pointer:search:") || got[0].MeasuredTokens != 5 {
		t.Fatalf("findings = %+v, %v; want one search finding over 5 sessions", got, err)
	}
	if !strings.Contains(got[0].Patch, "read `docs/deploy.md` first.") || !strings.Contains(got[0].Evidence, "`retryBackoff`") || !strings.Contains(got[0].Evidence, "ended in `docs/deploy.md`") {
		t.Fatalf("finding = %+v", got[0])
	}
	if four, _ := routing(docFiles(), huntRead(rows[len(rows)-5:len(rows)-1], fiveSessions[:4]...)...); len(four) != 0 {
		t.Fatalf("four sessions gave %+v, want the bar of 5", four)
	}
	diff, _ := routing(docFiles(), append(rows, cmd("s5", "rg other backend/"))...)
	if len(diff) != 1 {
		t.Fatalf("a different search must not be merged into the first: %+v", diff)
	}
}

func TestACPLocationsAndCodexCommandTextBothCount(t *testing.T) {
	got, err := routing(docFiles(), append(append(
		found("s1", "/w/worktrees/p/s1/docs/deploy.md"),
		cmd("s2", "sed -n '1,80p' ./docs/deploy.md"), cmd("s2", "rg deploy-2 .")),
		cmd("s3", "cat /repo/docs/deploy.md | head -20"), cmd("s3", "rg deploy-3 ."))...)
	if err != nil || len(got) != 1 || got[0].Fingerprint != "routing_pointer:doc:docs/deploy.md" || got[0].MeasuredTokens != 3 {
		t.Fatalf("findings = %+v, %v; want one doc over 3 sessions", got, err)
	}
}

func TestRowsWithoutDetailAreSkipped(t *testing.T) {
	rows := []store.ToolActivity{
		{SessionID: "s1", Kind: domain.ActivityKindMCPTool, ToolKind: "read"},
		{SessionID: "s2", Kind: domain.ActivityKindMCPTool, ToolKind: "read", Locations: "not json"},
		{SessionID: "s3", Kind: domain.ActivityKindMCPTool, ToolKind: "search", Input: "{}"},
		{SessionID: "s4", Kind: domain.ActivityKindCommand},
		cmd("s5", "cat"), cmd("s6", "rg"), cmd("s7", "go test ./..."),
		{SessionID: "e1", Kind: domain.ActivityKindMCPTool, ToolKind: "search", Input: `{"pattern":""}`},
		{SessionID: "e2", Kind: domain.ActivityKindMCPTool, ToolKind: "search", Input: `{"pattern":" "}`},
		cmd("e3", "rg ''"),
		cmd("t1", "rg foo"), cmd("t2", "grep -rn foo ."), cmd("t3", "rg -e foo src"),
		read("l1", ""), read("l2", ""), read("l3", ""),
		read("s8", "/elsewhere/docs/deploy.md"), read("s9", "../deploy.md"),
		{SessionID: "s10", Kind: domain.ActivityKindMCPTool, ToolKind: "read", Locations: `[{"path":"docs/deploy.md"}]`},
	}
	got, err := routing(docFiles(), rows...)
	if err != nil || len(got) != 0 {
		t.Fatalf("findings = %+v, %v; want none and no error", got, err)
	}
}

func TestDocNotOnDefaultBranchOrAlwaysLoadedIsSkipped(t *testing.T) {
	rows := []store.ToolActivity{read("s1", "docs/gone.md"), read("s2", "docs/gone.md"), read("s3", "docs/gone.md")}
	if got, _ := routing(docFiles(), rows...); len(got) != 0 {
		t.Fatalf("a doc missing from the default branch gave %+v", got)
	}
	own := []store.ToolActivity{read("s1", "AGENTS.md"), read("s2", "AGENTS.md"), read("s3", "AGENTS.md")}
	if got, _ := routing(docFiles(), own...); len(got) != 0 {
		t.Fatalf("an always-loaded file gave %+v", got)
	}
}

func TestPointerPatchLandsInTheArtifactDir(t *testing.T) {
	root := t.TempDir()
	s := New(Deps{ArtifactRoot: root, Log: slog.Default()})
	f := domain.ContextHealthFinding{ID: "f1", ProjectID: proj, Evidence: "e", Patch: "--- a/x\n"}
	dir := s.writeEvidence(f)
	data, err := os.ReadFile(filepath.Join(dir, patchFile))
	if err != nil || string(data) != "--- a/x\n" {
		t.Fatalf("patch artifact = %q, %v", data, err)
	}
}

func TestEvidenceStatesHowManySessionsWereSeen(t *testing.T) {
	got, _ := routing(docFiles(), foundRows("docs/deploy.md", "s1", "s2", "s3")...)
	if len(got) != 1 || !strings.Contains(got[0].Evidence, "recorded for 3 of 5 sessions active") {
		t.Fatalf("findings = %+v, want the coverage line with 3 of 5", got)
	}
	srch, _ := routing(docFiles(), huntRead([]store.ToolActivity{cmd("s1", "rg retryBackoff"), cmd("s2", "rg retryBackoff"), cmd("s3", "rg retryBackoff"), cmd("s4", "rg retryBackoff"), cmd("s5", "rg retryBackoff")}, fiveSessions...)...)
	if len(srch) != 1 || !strings.Contains(srch[0].Evidence, "recorded for 5 of 5 sessions active") {
		t.Fatalf("search findings = %+v, want the coverage line", srch)
	}
}

func TestGlobRowsAreNotSearches(t *testing.T) {
	glob := func(s string) store.ToolActivity {
		return store.ToolActivity{SessionID: domain.SessionID(s), Kind: domain.ActivityKindMCPTool, ToolKind: "search", Input: `{"pattern":"**/*.md","path":"/repo"}`}
	}
	grep := func(s string) store.ToolActivity {
		return store.ToolActivity{SessionID: domain.SessionID(s), Kind: domain.ActivityKindMCPTool, ToolKind: "search", Input: `{"pattern":"retryBackoff","output_mode":"content","-n":true}`}
	}
	if got, _ := routing(docFiles(), glob("s1"), glob("s2"), glob("s3")); len(got) != 0 {
		t.Fatalf("a glob in three sessions gave %+v", got)
	}
	if got, _ := routing(docFiles(), huntRead([]store.ToolActivity{grep("s1"), grep("s2"), grep("s3"), grep("s4"), grep("s5")}, fiveSessions...)...); len(got) != 1 {
		t.Fatalf("a grep in three sessions gave %+v, want one", got)
	}
}

func TestEveryRepeatedDocIsReportedSoOpenCardsSurviveHotterOnes(t *testing.T) {
	files := docFiles()
	var rows []store.ToolActivity
	for i := range 8 {
		name := "docs/doc-" + string(rune('a'+i)) + ".md"
		files[name] = "# D\n"
		for j := range 3 + i {
			rows = append(rows, found("s"+string(rune('a'+j)), name)...)
		}
	}
	got, err := routing(files, rows...)
	if err != nil || len(got) != 8 {
		t.Fatalf("findings = %d, %v; want all 8 so none already carded is dropped", len(got), err)
	}
}

func TestMentionNeedsAWholePath(t *testing.T) {
	files := docFiles()
	files["README.md"] = "# Readme\n"
	files["AGENTS.md"] = "# Agents\n\n- See backend/README.md for the API.\n"
	files["docs/rest-api.md"] = "# API\n"
	got, _ := routing(files, foundRows("docs/rest-api.md", "s1", "s2", "s3")...)
	if len(got) != 1 || !strings.Contains(got[0].Evidence, "no instruction file names") {
		t.Fatalf("findings = %+v, want api.md unlinked", got)
	}
	files["AGENTS.md"] = "# Agents\n\n- See backend/docs/rest-api.md for the API.\n"
	if got, _ := routing(files, foundRows("docs/rest-api.md", "s1", "s2", "s3")...); len(got) != 1 {
		t.Fatalf("backend/docs/rest-api.md is another doc: %+v", got)
	}
}

func TestPatchMidFileWithoutTrailingNewlineApplies(t *testing.T) {
	content := "a\nb\nc\nd"
	patch := insertPatch("F.md", content, 2, "NEW")
	assertPatchApplies(t, "F.md", content, patch, "b\nNEW\nc")
	end := insertPatch("F.md", content, 4, "TAIL")
	assertPatchApplies(t, "F.md", content, end, "d\nTAIL")
}

func TestProviderToolNameDecidesGlobVersusGrep(t *testing.T) {
	row := func(s, tool string) store.ToolActivity {
		return store.ToolActivity{SessionID: domain.SessionID(s), Kind: domain.ActivityKindMCPTool, ToolKind: "search", ProviderToolName: tool, Input: `{"pattern":"retryBackoff","path":"/repo"}`}
	}
	if got, _ := routing(docFiles(), huntRead([]store.ToolActivity{row("s1", "Grep"), row("s2", "Grep"), row("s3", "Grep"), row("s4", "Grep"), row("s5", "Grep")}, fiveSessions...)...); len(got) != 1 {
		t.Fatalf("a plain Grep (pattern and path only) gave %+v, want one finding", got)
	}
	if got, _ := routing(docFiles(), row("s1", "Glob"), row("s2", "Glob"), row("s3", "Glob")); len(got) != 0 {
		t.Fatalf("Glob gave %+v", got)
	}
}

// Q18-Q20 (B836): output filters, log greps and code lookups filed false cards.
func TestSearchesThatAreNotRepoLookupsGetNoCard(t *testing.T) {
	cases := map[string]string{
		"piped output filter":  "npx vitest run 2>&1 | grep -E 'Test Files|Tests '",
		"piped to rg":          "go test ./... | rg 'FAIL|panic'",
		"grep of tmp log":      "grep -E 'Test Files|Tests ' $TMPDIR/vitest.log",
		"grep of abs tmp":      "grep -n 'Test Files|Tests ' /tmp/out.txt",
		"grep of log file":     "grep -n 'Test Files|Tests ' run.log",
		"grep stdin no pipe":   "grep 'Test Files|Tests ' < out.txt",
		"grep of outside repo": "grep -rn 'Test Files|Tests ' /var/folders/x/out",
		"rg of home tallpine":  "rg 'Test Files|Tests ' ~/.tallpine/data/log",
	}
	for name, c := range cases {
		var rows []store.ToolActivity
		for _, s := range []string{"s1", "s2", "s3"} {
			rows = append(rows, read(s, "docs/deploy.md"), cmd(s, c))
		}
		if got, _ := routing(docFiles(), rows...); len(got) != 0 {
			t.Errorf("%s: %q gave %+v", name, c, got)
		}
	}
	// The Grep tool is held to the same rule.
	var tool []store.ToolActivity
	for _, s := range []string{"s1", "s2", "s3"} {
		tool = append(tool, read(s, "docs/deploy.md"), store.ToolActivity{SessionID: domain.SessionID(s), Kind: domain.ActivityKindMCPTool, ToolKind: "search", ProviderToolName: "Grep", Input: `{"pattern":"Test Files|Tests ","path":"/tmp/vitest.log"}`})
	}
	if got, _ := routing(docFiles(), tool...); len(got) != 0 {
		t.Errorf("Grep tool on a tmp log gave %+v", got)
	}
	// Absolute worktree and project paths count, even under .tallpine or /var.
	for _, p := range []string{"/Users/x/.tallpine/data/worktrees/p/s1/backend", "/var/folders/r/backend"} {
		var abs []store.ToolActivity
		for _, s := range fiveSessions {
			abs = append(abs, read(s, "docs/deploy.md"), cmd(s, "ls docs"), cmd(s, "grep -rn retryBackoff "+p))
		}
		r := RoutingFinder{Store: fakeActivity{abs, 5}, Reader: func(context.Context, domain.ProjectRecord) (Source, error) { return memSource(docFiles()), nil }}
		got, _ := r.Find(context.Background(), domain.ProjectRecord{ID: proj, Path: "/var/folders/r"}, Measurement{Groups: []group{{Report: Report{Files: []File{{Path: "AGENTS.md"}}}}}})
		if len(got) != 1 {
			t.Errorf("abs path %s gave %+v, want one", p, got)
		}
	}
	// Control: the same shape against repo files does file.
	var rows []store.ToolActivity
	for _, s := range fiveSessions {
		rows = append(rows, read(s, "docs/deploy.md"), cmd(s, "ls docs"), cmd(s, "grep -rn 'test summary' backend/"))
	}
	if got, _ := routing(docFiles(), rows...); len(got) != 1 {
		t.Errorf("repo grep gave %+v, want one", got)
	}
}

func TestCodeSymbolLookupWithoutDocReadGetsNoCard(t *testing.T) {
	var rows []store.ToolActivity
	for _, s := range []string{"s1", "s2", "s3"} {
		rows = append(rows, cmd(s, "rg -n quietWorkerReply backend/"))
	}
	if got, _ := routing(docFiles(), rows...); len(got) != 0 {
		t.Fatalf("a code-symbol lookup gave %+v", got)
	}
	// The lookup counts only for the session that then read a doc.
	if got, _ := routing(docFiles(), thenRead(rows, "s1", "s2")...); len(got) != 0 {
		t.Fatalf("two sessions reading docs gave %+v", got)
	}
}

func TestCardTitleIsGrammatical(t *testing.T) {
	in := askInput(domain.ContextHealthFinding{Kind: domain.ContextRoutingPointer, MeasuredTokens: 3, Fingerprint: "x"})
	if !strings.HasPrefix(in.Text, "3 doc-pointer gaps this week") {
		t.Fatalf("title = %q", in.Text)
	}
}
