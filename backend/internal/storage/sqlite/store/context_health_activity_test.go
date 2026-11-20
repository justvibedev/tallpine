package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestListToolActivityPullsDetailFieldsOfProjectSessions(t *testing.T) {
	s, session, conv := conversationFixture(t)
	ctx := context.Background()
	add := func(id string, kind domain.ActivityKind, detail string, at time.Time) {
		t.Helper()
		mustNoError(t, s.UpsertActivity(ctx, conv, "", domain.ConversationActivity{
			ID: id, Kind: kind, Status: domain.ActivityStatusCompleted, Detail: []byte(detail), ProviderItemID: id,
		}, at))
	}
	add("acp", domain.ActivityKindMCPTool, `{"toolKind":"read","providerToolName":"Read","locations":[{"path":"/w/docs/a.md"}],"input":{"pattern":"x"}}`, histClock)
	add("codex", domain.ActivityKindCommand, `{"command":"cat docs/a.md"}`, histClock.Add(time.Second))
	add("bad", domain.ActivityKindCommand, `not json`, histClock.Add(2*time.Second))
	add("old", domain.ActivityKindCommand, `{"command":"cat old.md"}`, histClock.Add(-48*time.Hour))
	add("reason", domain.ActivityKindReasoning, `{"command":"cat r.md"}`, histClock)

	got, err := s.ListToolActivity(ctx, "hist", histClock.Add(-time.Hour))
	mustNoError(t, err)
	if len(got) != 3 {
		t.Fatalf("rows = %+v, want acp, codex and the unreadable one only", got)
	}
	by := map[string]int{}
	for i, g := range got {
		if g.SessionID != session {
			t.Fatalf("session = %s, want %s", g.SessionID, session)
		}
		by[g.Command+g.ToolKind] = i
	}
	if _, ok := by["cat docs/a.md"]; !ok {
		t.Fatalf("codex command text missing: %+v", got)
	}
	acp := got[by["read"]]
	if acp.ProviderToolName != "Read" || acp.Locations == "" || acp.Input == "" || acp.Kind != domain.ActivityKindMCPTool {
		t.Fatalf("acp row = %+v, want locations and input", acp)
	}
	if other, _ := s.ListToolActivity(ctx, "nobody", histClock.Add(-time.Hour)); len(other) != 0 {
		t.Fatalf("another project's rows leaked: %+v", other)
	}
	if n, err := s.CountActiveSessions(ctx, "hist", time.Now().Add(-24*365*time.Hour)); err != nil || n != 1 {
		t.Fatalf("active sessions = %d, %v; want 1", n, err)
	}
	if n, _ := s.CountActiveSessions(ctx, "nobody", time.Now().Add(-24*365*time.Hour)); n != 0 {
		t.Fatalf("another project's sessions counted: %d", n)
	}
}
