package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const approvalsListJSON = `{"sessionId":"w-1","approvals":[{"requestId":"req-1","summary":"run","detail":{"command":"go build /tmp/x"},"decisions":[{"id":"a","label":"Allow","kind":"allow_once"},{"id":"al","label":"Always","kind":"allow_always"},{"id":"r","label":"Reject","kind":"reject_once"}]}],"inputs":[{"requestId":"in-1","summary":"Switch to Opus 4.8?","detail":{"inputMode":"form"}}]}`

func approvalsServer(t *testing.T, posted *string) Deps {
	cfg := setConfigEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			*posted = r.URL.Path + " " + string(b)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write([]byte(approvalsListJSON))
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
	return Deps{ProcessAlive: func(int) bool { return true }}
}

func TestSessionApprovalsListShowsExactCommand(t *testing.T) {
	var posted string
	out, _, err := executeCLI(t, approvalsServer(t, &posted), "session", "approvals", "w-1")
	if err != nil || !strings.Contains(out, "req-1") || !strings.Contains(out, "go build /tmp/x") {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestSessionApproveAndDenyPickOneTimeDecisions(t *testing.T) {
	var posted string
	deps := approvalsServer(t, &posted)
	if _, _, err := executeCLI(t, deps, "session", "approve", "w-1", "req-1", "--note", "tmp build"); err != nil {
		t.Fatal(err)
	}
	if posted != `/api/v1/sessions/w-1/conversation/approvals/req-1/resolve {"decisionId":"a","note":"tmp build"}` {
		t.Fatalf("approve posted %q", posted)
	}
	if _, _, err := executeCLI(t, deps, "session", "deny", "w-1", "req-1"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(posted, `"decisionId":"r"`) {
		t.Fatalf("deny posted %q", posted)
	}
}
