package artifactversions

import (
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestTrimAll_AppliesSmallerCapAndDropsIgnoredPaths(t *testing.T) {
	h := newHarness(t)
	h.cap.SetKeep(30)
	for i := 1; i <= 12; i++ {
		h.write("page.html", fmt.Sprintf("body-%02d", i))
		h.tick()
		h.tick()
	}
	if n := len(h.versions("page.html")); n != 12 {
		t.Fatalf("setup recorded %d versions, want 12", n)
	}
	// History recorded before venv/ was skipped.
	if _, _, err := h.store.AppendSessionArtifactVersion(h.ctx, domain.SessionArtifactVersion{
		SessionID: h.sess.ID, Path: "venv/lib/site.py", SHA256: sum("venv"), Size: 4, CreatedAt: time.Now().UTC(),
	}, 30); err != nil {
		t.Fatal(err)
	}

	h.cap.SetKeep(5)
	if err := h.cap.TrimAll(h.ctx, 0); err != nil {
		t.Fatalf("TrimAll: %v", err)
	}
	vs := h.versions("page.html")
	if len(vs) != 5 || vs[0].Version != 12 || vs[4].Version != 8 {
		t.Fatalf("got %d versions after trim (newest %d)", len(vs), vs[0].Version)
	}
	if n := h.blobCount(); n != 5 {
		t.Fatalf("blobs = %d, want 5", n)
	}
	if got := h.versions("venv/lib/site.py"); len(got) != 0 {
		t.Fatalf("ignored path kept %d versions", len(got))
	}
}

func TestSetKeep_BelowOneKeepsDefault(t *testing.T) {
	h := newHarness(t)
	h.cap.SetKeep(0)
	if h.cap.keep != DefaultKeepVersions {
		t.Fatalf("keep = %d, want %d", h.cap.keep, DefaultKeepVersions)
	}
}
