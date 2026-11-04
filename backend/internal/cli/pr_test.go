package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

const testHeadSHA = "0123456789abcdef0123456789abcdef01234567"

type mergeRoute struct {
	status int
	body   string
}

// mergeDaemon serves canned responses per "METHOD /path" and records requests.
func mergeDaemon(t *testing.T, routes map[string]mergeRoute) (*httptest.Server, *[]string, *string) {
	t.Helper()
	var reqs []string
	var mergeBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		reqs = append(reqs, key)
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			mergeBody = string(b)
		}
		route, ok := routes[key]
		if !ok {
			route = mergeRoute{http.StatusNotFound, `{"message":"nope","code":"NOT_FOUND"}`}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(route.status)
		_, _ = io.WriteString(w, route.body)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs, &mergeBody
}

func trackedPRRoutes(mergeStatus int, mergeResp string) map[string]mergeRoute {
	return map[string]mergeRoute{
		"GET /api/v1/sessions": {200, `{"sessions":[
			{"id":"s1","prs":[{"url":"https://github.com/o/r/pull/42","number":42}]},
			{"id":"s2","prs":[{"url":"https://github.com/o/r/pull/42","number":42},{"url":"https://github.com/o/r/pull/7","number":7}]}]}`},
		// The cached head differs from the reviewed one: only the reviewed head may be used.
		"GET /api/v1/sessions/s1/pr":      {200, `{"prs":[{"url":"https://github.com/o/r/pull/42","number":42,"headSha":"ffffffffffffffffffffffffffffffffffffffff"}]}`},
		"GET /api/v1/sessions/s1/reviews": {200, `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","prNumber":42,"latestRun":{"id":"r1","verdict":"approved","targetSha":"` + testHeadSHA + `","createdAt":"2026-01-01T00:00:00Z"}}]}`},
		"GET /api/v1/sessions/s2/reviews": {200, `{"reviews":[]}`},
		"POST /api/v1/prs/42/merge":       {mergeStatus, mergeResp},
	}
}

func TestPRMergeResolvesURLAndHeadSHAFromTrackedSessions(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _, mergeBody := mergeDaemon(t, trackedPRRoutes(http.StatusOK, `{"ok":true,"prNumber":42,"method":"squash"}`))
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "#42")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req apitypes.MergePRRequest
	if err := json.Unmarshal([]byte(*mergeBody), &req); err != nil {
		t.Fatalf("decode body %q: %v", *mergeBody, err)
	}
	if req.PRURL != "https://github.com/o/r/pull/42" || req.ExpectedHeadSHA != testHeadSHA {
		t.Fatalf("merge request = %+v", req)
	}
	if !strings.Contains(out, "merged PR #42 using squash") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestPRMergeWithExplicitFlagsSkipsLookup(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, reqs, mergeBody := mergeDaemon(t, map[string]mergeRoute{
		"POST /api/v1/prs/42/merge": {200, `{"ok":true,"prNumber":42}`},
	})
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "42",
		"--url", "https://github.com/o/r/pull/42", "--expected-sha", testHeadSHA)
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if len(*reqs) != 1 {
		t.Fatalf("requests = %v, want only the merge POST", *reqs)
	}
	if !strings.Contains(*mergeBody, testHeadSHA) || out != "merged PR #42\n" {
		t.Fatalf("body = %q, stdout = %q", *mergeBody, out)
	}
}

func TestPRMergeURLFlagDisambiguatesRepositories(t *testing.T) {
	cfg := setConfigEnv(t)
	routes := map[string]mergeRoute{
		"GET /api/v1/sessions": {200, `{"sessions":[
			{"id":"s1","prs":[{"url":"https://github.com/o/a/pull/42","number":42}]},
			{"id":"s2","prs":[{"url":"https://github.com/o/b/pull/42","number":42}]}]}`},
		"GET /api/v1/sessions/s2/reviews": {200, `{"reviews":[{"prUrl":"https://github.com/o/b/pull/42","prNumber":42,"latestRun":{"id":"r1","verdict":"approved","targetSha":"` + testHeadSHA + `"}}]}`},
		"POST /api/v1/prs/42/merge":       {200, `{"ok":true,"prNumber":42}`},
	}
	srv, _, mergeBody := mergeDaemon(t, routes)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if got := ExitCode(err); got != 2 || !strings.Contains(err.Error(), "--url") {
		t.Fatalf("ambiguous: exit = %d, err = %v", got, err)
	}
	if _, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "42", "--url", "https://github.com/o/b/pull/42"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if !strings.Contains(*mergeBody, "o/b/pull/42") || !strings.Contains(*mergeBody, testHeadSHA) {
		t.Fatalf("body = %q", *mergeBody)
	}
}

func TestPRMergeUntrackedPRFails(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, reqs, _ := mergeDaemon(t, map[string]mergeRoute{"GET /api/v1/sessions": {200, `{"sessions":[]}`}})
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if got := ExitCode(err); got != 1 || !strings.Contains(err.Error(), "not tracked") {
		t.Fatalf("exit = %d, err = %v", got, err)
	}
	for _, r := range *reqs {
		if strings.HasPrefix(r, "POST") {
			t.Fatalf("unexpected merge request: %v", *reqs)
		}
	}
}

func TestPRMergeRejectsInvalidNumber(t *testing.T) {
	setConfigEnv(t)

	for _, number := range []string{"0", "-1", "abc", "#"} {
		t.Run(number, func(t *testing.T) {
			_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", number)
			if got := ExitCode(err); got != 2 {
				t.Fatalf("exit code = %d, want 2; err=%v", got, err)
			}
		})
	}
}

func TestPRMergeRequiresExactlyOneArgument(t *testing.T) {
	setConfigEnv(t)

	for _, args := range [][]string{
		{"pr", "merge"},
		{"pr", "merge", "42", "43"},
	} {
		_, _, err := executeCLI(t, aliveDeps(), args...)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("args = %v, exit code = %d, want 2; err=%v", args, got, err)
		}
	}
}

func TestPRMergeSurfacesDaemonError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _, _ := mergeDaemon(t, trackedPRRoutes(http.StatusConflict, `{"message":"PR is not mergeable","code":"PR_NOT_MERGEABLE","requestId":"req-1"}`))
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1; err=%v", got, err)
	}
	for _, want := range []string{"PR is not mergeable", "PR_NOT_MERGEABLE", "req-1"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want %q", err, want)
		}
	}
}

func TestPRMergeNamesFailedPrecondition(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _, _ := mergeDaemon(t, trackedPRRoutes(http.StatusUnprocessableEntity, `{"message":"PR merge preconditions are not met: CI status is unknown","code":"PR_PRECONDITIONS_UNMET","details":{"reason":"ci_unknown","detail":"CI status is unknown"}}`))
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if got := ExitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1; err=%v", got, err)
	}
	for _, want := range []string{"CI status is unknown", "PR_PRECONDITIONS_UNMET", "reason: ci_unknown"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want %q", err, want)
		}
	}
}

func TestPRMergeShowsOperationFailureCause(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _, _ := mergeDaemon(t, trackedPRRoutes(http.StatusInternalServerError, `{"message":"PR operation failed: merge pull request: github scm: 502 Bad Gateway","code":"PR_OPERATION_FAILED","details":{"cause":"merge pull request: github scm: 502 Bad Gateway"}}`))
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if err == nil || !strings.Contains(err.Error(), "502 Bad Gateway") || !strings.Contains(err.Error(), "PR_OPERATION_FAILED") {
		t.Fatalf("err = %v, want the operation failure cause", err)
	}
}

func TestPRResolveCommentsPostsIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"ok":true,"resolved":2}`)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments", "42", "thread-1", "thread-2")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/prs/42/resolve-comments" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	var req resolveCommentsRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(req.CommentIDs) != 2 || req.CommentIDs[0] != "thread-1" || req.CommentIDs[1] != "thread-2" {
		t.Fatalf("comment ids = %v", req.CommentIDs)
	}
	if !strings.Contains(out, "resolved 2 review thread(s) on PR #42") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestPRResolveCommentsAllowsNoIDs(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := reviewServer(t, http.StatusOK, `{"ok":true,"resolved":3}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments", "42"); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if strings.TrimSpace(capture.body) != "{}" {
		t.Fatalf("body = %q, want {}", capture.body)
	}
}

func TestPRResolveCommentsRequiresPRNumber(t *testing.T) {
	setConfigEnv(t)

	_, _, err := executeCLI(t, aliveDeps(), "pr", "resolve-comments")
	if got := ExitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2; err=%v", got, err)
	}
}

func TestPRMergeRefusesWithoutApprovedReview(t *testing.T) {
	for name, reviews := range map[string]string{
		"no reviews":             `{"reviews":[]}`,
		"changes requested":      `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","latestRun":{"id":"r1","verdict":"changes_requested","targetSha":"` + testHeadSHA + `"}}]}`,
		"running with no prior":  `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","latestRun":{"id":"r1","status":"running","targetSha":"` + testHeadSHA + `"}}]}`,
		"newer changes request":  `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","latestRun":{"id":"r2","verdict":"changes_requested","targetSha":"bbbb"},"previousRun":{"id":"r1","verdict":"approved","targetSha":"` + testHeadSHA + `"}}]}`,
		"approved other PR":      `{"reviews":[{"prUrl":"https://github.com/o/r/pull/9","latestRun":{"id":"r1","verdict":"approved","targetSha":"` + testHeadSHA + `"}}]}`,
		"approved without a sha": `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","latestRun":{"id":"r1","verdict":"approved"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			routes := trackedPRRoutes(http.StatusOK, `{"ok":true,"prNumber":42}`)
			routes["GET /api/v1/sessions/s1/reviews"] = mergeRoute{200, reviews}
			srv, reqs, _ := mergeDaemon(t, routes)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
			if err == nil || !strings.Contains(err.Error(), "Tallpine-approved review") {
				t.Fatalf("err = %v, want refusal naming the missing Tallpine-approved review", err)
			}
			for _, r := range *reqs {
				if strings.HasPrefix(r, "POST") {
					t.Fatalf("merge was posted without a reviewed head: %v", *reqs)
				}
			}
		})
	}
}

func TestPRMergePinsToHeadOfApprovedRunWhileRereviewRuns(t *testing.T) {
	cfg := setConfigEnv(t)
	routes := trackedPRRoutes(http.StatusOK, `{"ok":true,"prNumber":42}`)
	routes["GET /api/v1/sessions/s1/reviews"] = mergeRoute{200, `{"reviews":[{"prUrl":"https://github.com/o/r/pull/42","latestRun":{"id":"r2","status":"running","targetSha":"bbbb"},"previousRun":{"id":"r1","verdict":"approved","targetSha":"` + testHeadSHA + `"}}]}`}
	srv, _, mergeBody := mergeDaemon(t, routes)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, aliveDeps(), "pr", "merge", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(*mergeBody, testHeadSHA) || !strings.Contains(errOut, testHeadSHA) {
		t.Fatalf("body = %q, stderr = %q, want the approved head pinned and echoed", *mergeBody, errOut)
	}
}
