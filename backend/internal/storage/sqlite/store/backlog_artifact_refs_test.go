package store_test

import (
	"context"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestLiveBacklogArtifactRefs_OnlyLiveItems(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	live := addBacklog(t, s, "p1", "live", domain.BacklogOpen, "")
	done := addBacklog(t, s, "p1", "done", domain.BacklogDone, "")
	for _, c := range []struct {
		id   string
		note string
		link domain.BacklogLink
	}{
		{live.ID, "design: agent-orchestrator-494/B202.md", domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: "sess-live"}},
		{live.ID, "", domain.BacklogLink{Kind: domain.BacklogLinkArtifact, Ref: "art-live/doc.md"}},
		{done.ID, "", domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: "sess-done"}},
	} {
		if _, err := s.AddBacklogLink(ctx, c.id, c.link, backlogNow); err != nil {
			t.Fatal(err)
		}
	}
	for id, note := range map[string]string{live.ID: "design: agent-orchestrator-494/B202.md", done.ID: "old-note-ref/x.md"} {
		if _, err := s.UpdateBacklogItem(ctx, id, backlogNow, func(it *domain.BacklogItem) error { it.Note = note; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	sessions, artifacts, texts, err := s.LiveBacklogArtifactRefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sessions, []string{"sess-live"}) || !slices.Equal(artifacts, []string{"art-live/doc.md"}) {
		t.Fatalf("sessions %v artifacts %v", sessions, artifacts)
	}
	if !slices.Contains(texts, "design: agent-orchestrator-494/B202.md") || slices.Contains(texts, "old-note-ref/x.md") {
		t.Fatalf("texts %q", texts)
	}
}
