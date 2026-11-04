package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/officialimport"
)

func importOfficialServer(t *testing.T, report officialimport.Report) (*httptest.Server, *apitypes.AOImportRequest) {
	t.Helper()
	var got apitypes.AOImportRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/imports/ao" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(importOfficialResponse{Report: report})
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func sampleImportReport() officialimport.Report {
	return officialimport.Report{
		SourceDataDir: "/home/u/.ao/data", Found: true, DryRun: true,
		Projects: []officialimport.ProjectPlan{
			{ID: "alpha", Name: "Alpha App", Path: "/w/alpha", Kind: "single_repo", Action: officialimport.ActionCreate, Selected: true,
				ConfigItems: []string{"worker", "env (2 vars)"}, Warnings: []string{`worker harness "gemini" is not available in Tallpine; using claude-code`}},
			{ID: "beta", Path: "/w/beta", Kind: "workspace", ChildRepos: 3, Action: officialimport.ActionUnchanged},
			{ID: "old", Path: "/w/old", Action: officialimport.ActionSkip, Reason: "archived in Agent Orchestrator"},
		},
		Skipped: []officialimport.Skipped{{What: "sessions", Why: "live runtime state", Count: 12}},
		Counts:  officialimport.Counts{Create: 1, Unchanged: 1, Skip: 1},
	}
}

func TestImportOfficialDryRunPrintsPlan(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, got := importOfficialServer(t, sampleImportReport())
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "import", "ao", "--dry-run")
	if err != nil {
		t.Fatalf("err=%v stderr=%s", err, errOut)
	}
	if !got.DryRun || len(got.ProjectIDs) != 0 || got.IncludeSettings {
		t.Fatalf("request = %+v", got)
	}
	for _, want := range []string{"Dry run -- nothing was written.", "(read-only)", "create", "alpha", "Alpha App",
		"[workspace, 3 repos]", `worker harness "gemini"`, "Not imported:", "sessions (12)", "1 create, 0 update, 1 unchanged"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestImportOfficialSelectiveRunSendsProjectIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	rep := sampleImportReport()
	rep.DryRun = false
	srv, got := importOfficialServer(t, rep)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"import", "ao", "--project", "alpha", "--project", "beta", "--settings", "--from-data-dir", "/tmp/snap")
	if err != nil {
		t.Fatal(err)
	}
	if got.DryRun || strings.Join(got.ProjectIDs, ",") != "alpha,beta" || !got.IncludeSettings || got.SourceDataDir == "" {
		t.Fatalf("request = %+v", got)
	}
}

func TestImportOfficialRealRunNeedsExplicitScope(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "import", "ao")
	var ue usageError
	if err == nil || !asUsageError(err, &ue) {
		t.Fatalf("err = %v, want usage error", err)
	}
	_, _, err = executeCLI(t, Deps{}, "import", "ao", "--all", "--project", "x")
	if err == nil || !asUsageError(err, &ue) {
		t.Fatalf("err = %v, want usage error", err)
	}
}

func TestImportOfficialFailsWhenAProjectErrored(t *testing.T) {
	cfg := setConfigEnv(t)
	rep := sampleImportReport()
	rep.Counts.Error = 1
	srv, _ := importOfficialServer(t, rep)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "import", "ao", "--all")
	if err == nil {
		t.Fatal("expected a non-zero exit when a project failed")
	}
}

func TestImportOfficialMissingSourceIsReported(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := importOfficialServer(t, officialimport.Report{SourceDataDir: "/nope", DryRun: true})
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "import", "ao", "--dry-run")
	if err == nil || !strings.Contains(out, "No Agent Orchestrator database found") {
		t.Fatalf("err=%v out=%s", err, out)
	}
}

func asUsageError(err error, target *usageError) bool {
	return errors.As(err, target)
}
