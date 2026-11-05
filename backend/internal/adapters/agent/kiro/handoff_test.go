package kiro

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A started Kiro chat must expose its ACP session id as the conversation the
// terminal resumes, and a terminal must expose its hook-reported id, both in
// the canonical sess_ form (kiro-cli 3.0.0 session/load on a bare id silently
// starts an empty session).
func TestNativeConversationIDUsesSurfaceIdentityInCanonicalForm(t *testing.T) {
	p := &Plugin{}
	ref := ports.SessionRef{Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "c592d761-tui"}}
	cases := []struct {
		name     string
		mode     domain.SessionMode
		provider string
		want     string
	}{
		{"chat uses ACP session id", domain.SessionModeChat, " sess_acp-1 ", "sess_acp-1"},
		{"terminal canonicalizes bare hook id", domain.SessionModeTUI, "sess_ignored", "sess_c592d761-tui"},
		{"chat without session", domain.SessionModeChat, "", ""},
		{"rejects path-like ids", domain.SessionModeChat, "../h1/sess_x", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok, err := p.NativeConversationID(context.Background(), ref, tc.mode, tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			if id != tc.want || ok != (tc.want != "") {
				t.Fatalf("got (%q, %v), want %q", id, ok, tc.want)
			}
		})
	}
}

// ACP session/new writes only session.json; history exists once a turn wrote
// messages.jsonl. Only a reserved-only v3 session reads as absent, so a switch
// can start fresh. An id the v3 store does not hold (a v2-created session v3
// still resumes) must read as present, or every restore of it would relaunch
// fresh and drop the conversation.
func TestNativeConversationExistsRequiresPersistedTurns(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, root, "h1", "sess_with-turns", time.Now(), "1")
	empty := filepath.Join(root, "h2", "sess_reserved")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, "session.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{sessionsRoot: root}
	for id, want := range map[string]bool{
		"sess_with-turns": true,
		"with-turns":      true,
		"sess_reserved":   false,
		"sess_v2-only":    true,
		"":                false,
		"../h2/sess_x":    false,
	} {
		got, err := p.NativeConversationExists(context.Background(), ports.SessionRef{}, id, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("NativeConversationExists(%q) = %v, want %v", id, got, want)
		}
	}
}
