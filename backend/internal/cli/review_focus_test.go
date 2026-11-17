package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B743: --focus reaches the trigger request; the CLI never sends an author.
func TestReviewTriggerForwardsFocus(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, capture := reviewServer(t, http.StatusCreated, `{"created":true}`)
	writeRunFileFor(t, cfg, srv)

	if _, errOut, err := executeCLI(t, aliveDeps(), "review", "trigger", "mer-1", "--focus", "  check the migration  "); err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(capture.body), &body); err != nil {
		t.Fatalf("decode body %q: %v", capture.body, err)
	}
	if body["focus"] != "check the migration" {
		t.Fatalf("body = %v, want the trimmed focus", body)
	}
	if _, ok := body["focusAuthor"]; ok {
		t.Fatalf("body = %v, want no author: the daemon derives it", body)
	}
}

// B743: an over-long --focus is a usage error before any request.
func TestReviewTriggerRefusesOverlongFocus(t *testing.T) {
	cfg := setReviewEnv(t)
	srv, capture := reviewServer(t, http.StatusCreated, `{"created":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, aliveDeps(), "review", "trigger", "mer-1", "--focus", strings.Repeat("x", domain.MaxReviewFocusChars+1))
	if got := ExitCode(err); got != 2 || !strings.Contains(err.Error(), "--focus") {
		t.Fatalf("exit = %d err = %v, want a usage error naming --focus", got, err)
	}
	if capture.body != "" {
		t.Fatalf("request sent: %q", capture.body)
	}
}
