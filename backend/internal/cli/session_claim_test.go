package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestSessionClaimPR_ProjectScopeMismatchIsUsage(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "https://github.com/justvibedev/tallpine-dev/pull/142", "-p", "other")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "session demo-1 is not in project other") {
		t.Fatalf("err=%v exit=%d, want project mismatch usage", err, ExitCode(err))
	}
	want := []string{"GET /api/v1/sessions/demo-1"}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests=%#v want %#v", got, want)
	}
}

func TestSessionClaimPR_UsesCurrentSessionFromEnv(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "demo-1")
	cfg := setConfigEnv(t)
	srv, log := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "142")
	if err != nil {
		t.Fatalf("claim-pr with TALLPINE_SESSION_ID failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "claimed PR #142") {
		t.Fatalf("unexpected output: %s", out)
	}
	want := []string{
		"GET /api/v1/sessions/demo-1",
		"GET /api/v1/projects/demo",
		"POST /api/v1/sessions/demo-1/pr/claim",
	}
	if got := log.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests=%#v want %#v", got, want)
	}
}

func TestSessionClaimPR_OneArgRequiresCurrentSessionEnv(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "")
	setConfigEnv(t)

	_, _, err := executeCLI(t, Deps{}, "session", "claim-pr", "142")
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want usage error", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "pass <session-id> or set TALLPINE_SESSION_ID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSessionClaimPR_HelpDocumentsCurrentSessionShorthand(t *testing.T) {
	out, _, err := executeCLI(t, Deps{}, "session", "claim-pr", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"claim-pr [<session-id>] <pr-ref>",
		"current session is read from TALLPINE_SESSION_ID",
		"tallpine session claim-pr 88",
		"tallpine session claim-pr mer-3 88",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %q:\n%s", want, out)
		}
	}
}

func TestSessionClaimPR_JSONAndNoTakeoverError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := sessionCommandServer(t)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "https://github.com/justvibedev/tallpine-dev/pull/142", "--json")
	if err != nil {
		t.Fatalf("claim-pr --json failed: %v stderr=%s", err, errOut)
	}
	var got apitypes.ClaimPRResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.SessionID != "demo-1" || len(got.PRs) != 1 || got.PRs[0].Number != 142 {
		t.Fatalf("bad json err=%v got=%#v out=%s", err, got, out)
	}

	_, _, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "https://github.com/justvibedev/tallpine-dev/pull/142", "--no-takeover")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "PR_CLAIMED_BY_ACTIVE_SESSION") {
		t.Fatalf("err=%v exit=%d, want takeover refusal runtime error", err, ExitCode(err))
	}
}

// TestSessionClaimPR_Draft covers #4171 from the CLI side: claiming a draft PR
// succeeds and the draft state survives into `--json` output instead of the
// daemon answering PR_NOT_OPEN.
func TestSessionClaimPR_Draft(t *testing.T) {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://github.com/justvibedev/tallpine-dev","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			var req apitypes.ClaimPRRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":`+jsonQuote(req.PR)+`,"number":4168,"state":"draft","ci":"pending","review":"none","mergeability":"unknown","reviewComments":false,"updatedAt":"2026-08-20T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "4168")
	if err != nil {
		t.Fatalf("claim-pr draft failed: %v stderr=%s", err, errOut)
	}
	if !strings.Contains(out, "claimed PR #4168") {
		t.Fatalf("unexpected output: %s", out)
	}

	out, errOut, err = executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "4168", "--json")
	if err != nil {
		t.Fatalf("claim-pr draft --json failed: %v stderr=%s", err, errOut)
	}
	var got apitypes.ClaimPRResponse
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad json err=%v out=%s", err, out)
	}
	if len(got.PRs) != 1 || got.PRs[0].State != "draft" {
		t.Fatalf("claim response = %#v, want a single draft PR", got.PRs)
	}
}

func TestSessionClaimPR_GitLabMR(t *testing.T) {
	cfg := setConfigEnv(t)
	var capturedReq apitypes.ClaimPRRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://gitlab.com/castai/ctxd","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			_ = json.NewDecoder(r.Body).Decode(&capturedReq)
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":"`+capturedReq.PR+`","number":9,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "https://gitlab.com/castai/ctxd/-/merge_requests/9")
	if err != nil {
		t.Fatalf("claim-pr gitlab failed: %v", err)
	}
	if capturedReq.PR != "https://gitlab.com/castai/ctxd/-/merge_requests/9" {
		t.Fatalf("claim request PR = %q, want gitlab MR URL", capturedReq.PR)
	}
}

func TestSessionClaimPR_GitLabNumericRef(t *testing.T) {
	cfg := setConfigEnv(t)
	var capturedReq apitypes.ClaimPRRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"https://gitlab.com/castai/ctxd","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			_ = json.NewDecoder(r.Body).Decode(&capturedReq)
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":"`+capturedReq.PR+`","number":9,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "claim-pr", "demo-1", "9")
	if err != nil {
		t.Fatalf("claim-pr gitlab numeric failed: %v", err)
	}
	if capturedReq.PR != "https://gitlab.com/castai/ctxd/-/merge_requests/9" {
		t.Fatalf("claim request PR = %q, want expanded gitlab MR URL", capturedReq.PR)
	}
}

func TestSessionClaimPR_GHFallbackWhenProjectRepoMissing(t *testing.T) {
	cfg := setConfigEnv(t)
	log := &sessionRequestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.append(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "working", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"status":"ok","project":{"id":"demo","name":"Demo","path":"/repo/demo","repo":"","defaultBranch":"main"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions/demo-1/pr/claim":
			var req apitypes.ClaimPRRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","prs":[{"url":`+jsonQuote(req.PR)+`,"number":142,"state":"open","ci":"passing","review":"review_required","mergeability":"mergeable","reviewComments":false,"updatedAt":"2026-06-04T12:00:00Z"}],"branchChanged":false,"takenOverFrom":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	var ghDir string
	out, _, err := executeCLI(t, Deps{
		ProcessAlive: func(int) bool { return true },
		CommandOutputInDir: func(_ context.Context, dir, name string, args ...string) ([]byte, error) {
			ghDir = dir
			if name != "gh" {
				t.Fatalf("command name=%s", name)
			}
			return []byte("https://github.com/justvibedev/tallpine-dev\n"), nil
		},
	}, "session", "claim-pr", "demo-1", "142")
	if err != nil {
		t.Fatalf("claim-pr fallback failed: %v", err)
	}
	if ghDir != "/repo/demo" || !strings.Contains(out, "claimed PR #142") {
		t.Fatalf("ghDir=%q out=%s", ghDir, out)
	}
}
