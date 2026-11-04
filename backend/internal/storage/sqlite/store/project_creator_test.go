package store_test

import (
	"context"
	"testing"
	"time"
)

func TestProjectCreatorRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, ok, err := s.ProjectCreator(ctx, "lab"); err != nil || ok {
		t.Fatalf("unrecorded project: ok=%v err=%v, want not found", ok, err)
	}
	if err := s.SetProjectCreator(ctx, "lab", "app", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := s.ProjectCreator(ctx, "lab"); err != nil || !ok || got != "app" {
		t.Fatalf("creator = %q ok=%v err=%v, want app", got, ok, err)
	}
}

func TestProjectCreatorNeverOverwritesAndArchiveClearsIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "lab")
	if err := s.SetProjectCreator(ctx, "lab", "app", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectCreator(ctx, "lab", "web", time.Now()); err == nil {
		t.Fatal("a second creator must be refused")
	}
	if got, _, _ := s.ProjectCreator(ctx, "lab"); got != "app" {
		t.Fatalf("creator = %q, want app (never overwritten)", got)
	}
	if ok, err := s.ArchiveProject(ctx, "lab", time.Now()); err != nil || !ok {
		t.Fatalf("archive ok=%v err=%v", ok, err)
	}
	if _, ok, _ := s.ProjectCreator(ctx, "lab"); ok {
		t.Fatal("archive left the creator row")
	}
}
