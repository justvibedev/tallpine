package sessiontoken

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestSessionTokenIsBoundToSessionAndLaunch(t *testing.T) {
	a := New("install-token")
	token := a.IssueSession("acme.dev-1", "verifier-1")
	if !strings.HasPrefix(token, "vos1.acme.dev-1.") {
		t.Fatalf("token = %q", token)
	}
	if !a.VerifySession(token, "acme.dev-1", "verifier-1") {
		t.Fatal("token does not verify for its own launch")
	}
	if a.VerifySession(token, "acme.dev-1", "verifier-2") {
		t.Fatal("token verified for a later launch of the session")
	}
	if a.VerifySession(token, "acme.dev-2", "verifier-1") {
		t.Fatal("token verified for another session")
	}
	if New("rotated-install-token").VerifySession(token, "acme.dev-1", "verifier-1") {
		t.Fatal("token survived an install-token rotation")
	}
	if !New("install-token").VerifySession(token, "acme.dev-1", "verifier-1") {
		t.Fatal("token did not survive a daemon restart with the same install token")
	}
}

func TestReviewerTokenIsBoundToReviewAndWorker(t *testing.T) {
	a := New("install-token")
	token := a.IssueReviewer("rev-1", "app-1")
	if !a.VerifyReviewer(token, "rev-1", "app-1") {
		t.Fatal("reviewer token does not verify")
	}
	if a.VerifyReviewer(token, "rev-1", "app-2") || a.VerifyReviewer(token, "rev-2", "app-1") {
		t.Fatal("reviewer token verified for another review or worker")
	}
	if a.VerifySession(strings.Replace(token, "vor1.", "vos1.", 1), "rev-1", "app-1") {
		t.Fatal("a reviewer token passed as a session token")
	}
}

func TestIssueRefusesUnverifiableBindings(t *testing.T) {
	var none *Authority
	if New("") != nil || New("  ") != nil {
		t.Fatal("an empty install token produced an authority")
	}
	for name, got := range map[string]string{
		"nil authority":  none.IssueSession("app-1", "v"),
		"no verifier":    New("k").IssueSession("app-1", ""),
		"no session":     New("k").IssueSession("", "v"),
		"slash in id":    New("k").IssueSession("a/b", "v"),
		"no worker":      New("k").IssueReviewer("rev-1", ""),
		"nil reviewer":   none.IssueReviewer("rev-1", "app-1"),
		"escaped id":     New("k").IssueSession("a%2Fb", "v"),
		"whitespace id":  New("k").IssueSession(domain.SessionID("a b"), "v"),
		"non-ascii id":   New("k").IssueSession("sé-1", "v"),
		"control in id":  New("k").IssueSession("a\nb", "v"),
		"overlong id":    New("k").IssueSession(domain.SessionID(strings.Repeat("a", 257)), "v"),
		"empty reviewer": New("k").IssueReviewer("", "app-1"),
	} {
		if got != "" {
			t.Errorf("%s: issued %q", name, got)
		}
	}
	if none.VerifySession("vos1.app-1.x", "app-1", "v") {
		t.Fatal("nil authority verified a token")
	}
}

func TestParse(t *testing.T) {
	a := New("k")
	for _, tc := range []struct {
		token string
		kind  Kind
		id    string
		ok    bool
	}{
		{a.IssueSession("acme.dev-7", "v"), KindSession, "acme.dev-7", true},
		{a.IssueReviewer("rev-1", "app-1"), KindReviewer, "rev-1", true},
		{"vos1.app-1.", "", "", false},
		{"vos1..mac", "", "", false},
		{"vos1.app-1", "", "", false},
		{"vos1.a/b.mac", "", "", false},
		{"plain-install-token", "", "", false},
		{"", "", "", false},
	} {
		kind, id, ok := Parse(tc.token)
		if kind != tc.kind || id != tc.id || ok != tc.ok {
			t.Errorf("Parse(%q) = %q, %q, %v; want %q, %q, %v", tc.token, kind, id, ok, tc.kind, tc.id, tc.ok)
		}
		if IsScoped(tc.token) != tc.ok {
			t.Errorf("IsScoped(%q) = %v", tc.token, !tc.ok)
		}
	}
}

func TestBoundTo(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")
	if err := os.WriteFile(runFile, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		issuer, target string
		want           bool
	}{
		{runFile, runFile, true},
		{dir + "/./running.json", runFile, true},
		{filepath.Join(link, "running.json"), runFile, true},
		{runFile, filepath.Join(t.TempDir(), "running.json"), false},
		{"", runFile, false},
		{runFile, "", false},
	} {
		if got := BoundTo(tc.issuer, tc.target); got != tc.want {
			t.Errorf("BoundTo(%q, %q) = %v, want %v", tc.issuer, tc.target, got, tc.want)
		}
	}
}
