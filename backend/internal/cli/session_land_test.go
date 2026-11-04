package cli

import (
	"strings"
	"testing"
)

func runSessionLand(t *testing.T, routes map[string]mergeRoute, args ...string) (string, []string, string, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv, reqs, body := mergeDaemon(t, routes)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, aliveDeps(), append([]string{"session", "land", "s1"}, args...)...)
	return out, *reqs, *body, err
}

func sessionLandRoutes(resp string) map[string]mergeRoute {
	return map[string]mergeRoute{
		"POST /api/v1/sessions/s1/land": {200, resp},
		"POST /api/v1/sessions/s1/kill": {200, `{"sessionId":"s1","freed":true}`},
	}
}

func TestSessionLandDryRunMovesNothingAndKeepsWorker(t *testing.T) {
	routes := sessionLandRoutes(`{"target":"main","oldSha":"1111111111111111","newSha":"","dryRun":true}`)
	out, reqs, body, err := runSessionLand(t, routes, "--dry-run", "--kill-worker", "--into", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"dryRun":true`) || !strings.Contains(body, `"into":"feature"`) {
		t.Fatalf("body = %s", body)
	}
	if contains(reqs, "POST /api/v1/sessions/s1/kill") || !strings.Contains(out, "dry-run: would squash s1 onto main") {
		t.Fatalf("reqs=%v out=%q", reqs, out)
	}
}

func TestSessionLandPrintsResultAndKillsWorker(t *testing.T) {
	routes := sessionLandRoutes(`{"target":"main","oldSha":"1111111111111111","newSha":"2222222222222222","pushed":true,"updatedCheckouts":["/repo"]}`)
	out, reqs, _, err := runSessionLand(t, routes, "--kill-worker")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"landed s1 on main", "pushed to origin/main", "fast-forwarded checkout /repo"} {
		if !strings.Contains(out, want) {
			t.Fatalf("out lacks %q: %q", want, out)
		}
	}
	if !contains(reqs, "POST /api/v1/sessions/s1/kill") {
		t.Fatalf("reqs=%v", reqs)
	}
}

func TestSessionLandSurfacesRefusal(t *testing.T) {
	routes := map[string]mergeRoute{"POST /api/v1/sessions/s1/land": {409, `{"message":"not approved: head differs","code":"LAND_NOT_APPROVED"}`}}
	_, _, _, err := runSessionLand(t, routes)
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("err = %v", err)
	}
}
