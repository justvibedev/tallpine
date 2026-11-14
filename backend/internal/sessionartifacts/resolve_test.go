package sessionartifacts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestProjectFolderAndDir(t *testing.T) {
	cases := map[string]string{"tower-wars-12": "tower-wars", "dpf-1": "dpf", "plan-exp-2-7": "plan-exp-2", "weird": "_", "x-": "_", "x-a1": "_"}
	for id, want := range cases {
		if got := ProjectFolder(domain.SessionID(id)); got != want {
			t.Errorf("ProjectFolder(%q)=%q want %q", id, got, want)
		}
	}
	if got, want := Dir("/d", "tower-wars-3"), filepath.Join("/d", "artifacts", "tower-wars", "tower-wars-3"); got != want {
		t.Errorf("Dir=%q want %q", got, want)
	}
}

func TestResolve(t *testing.T) {
	d := "/data"
	for name, c := range map[string]struct{ stored, want string }{
		"new session, nothing stored": {"", Dir(d, "tw-2")},
		"canonical stored":            {Dir(d, "tw-2"), Dir(d, "tw-2")},
		"legacy flat stays flat":      {LegacyDir(d, "tw-2"), LegacyDir(d, "tw-2")},
		"custom dir is followed":      {"/elsewhere/artifacts/tw-2", "/elsewhere/artifacts/tw-2"},
	} {
		if got := Resolve(d, "tw-2", c.stored); got != c.want {
			t.Errorf("%s: Resolve = %q, want %q", name, got, c.want)
		}
	}
	// Deleters never follow a custom dir.
	if got := ResolveManaged(d, "tw-2", "/elsewhere/artifacts/tw-2"); got != Dir(d, "tw-2") {
		t.Errorf("ResolveManaged followed a custom dir: %q", got)
	}
	if got := ResolveManaged(d, "tw-2", LegacyDir(d, "tw-2")); got != LegacyDir(d, "tw-2") {
		t.Errorf("ResolveManaged dropped the legacy dir: %q", got)
	}
	if !IsManaged(d, "tw-2", "") || !IsManaged(d, "tw-2", LegacyDir(d, "tw-2")) || IsManaged(d, "tw-2", "/x/tw-2") {
		t.Error("IsManaged wrong")
	}
}

type getter struct {
	rec domain.SessionRecord
	ok  bool
}

func (g getter) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return g.rec, g.ok, nil
}

func TestResolveForID(t *testing.T) {
	d := "/data"
	rec := domain.SessionRecord{ID: "tw-2"}
	rec.Metadata.ArtifactDir = LegacyDir(d, "tw-2")
	if got := ResolveForID(context.Background(), d, getter{rec, true}, "tw-2", false); got != LegacyDir(d, "tw-2") {
		t.Errorf("legacy row resolved to %q", got)
	}
	if got := ResolveForID(context.Background(), d, getter{}, "tw-2", true); got != Dir(d, "tw-2") {
		t.Errorf("unknown session resolved to %q", got)
	}
}

func TestCollidesWithLegacySession(t *testing.T) {
	data := t.TempDir()
	flat := LegacyDir(data, "plan-exp-2")
	if err := os.MkdirAll(flat, 0o700); err != nil {
		t.Fatal(err)
	}
	if CollidesWithLegacySession(data, "plan-exp-2-1") {
		t.Error("an empty folder is not a collision")
	}
	if err := os.WriteFile(filepath.Join(flat, "r.html"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !CollidesWithLegacySession(data, "plan-exp-2-1") {
		t.Error("a flat session dir holding files is a collision")
	}
	if err := os.MkdirAll(filepath.Join(flat, "plan-exp-2-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if CollidesWithLegacySession(data, "plan-exp-2-2") {
		t.Error("a real project folder is not a collision")
	}
}

// A row from before the artifact_dir column (empty stored dir) with files at the
// flat path resolves there, not to the canonical path.
func TestResolveEmptyStoredFindsTheFlatDir(t *testing.T) {
	data := t.TempDir()
	if got := Resolve(data, "tw-2", ""); got != Dir(data, "tw-2") {
		t.Fatalf("no flat dir: Resolve = %q, want the canonical path", got)
	}
	flat := LegacyDir(data, "tw-2")
	if err := os.MkdirAll(flat, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"Resolve":        Resolve(data, "tw-2", ""),
		"ResolveManaged": ResolveManaged(data, "tw-2", ""),
		"ResolveForID":   ResolveForID(context.Background(), data, getter{domain.SessionRecord{ID: "tw-2"}, true}, "tw-2", false),
	} {
		if got != flat {
			t.Errorf("%s = %q, want the flat dir %q", name, got, flat)
		}
	}
	// A project folder that happens to be named like the session is not its dir.
	if err := os.MkdirAll(filepath.Join(data, "artifacts", "tw-2", "tw-2-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := Resolve(data, "tw-2", ""); got != Dir(data, "tw-2") {
		t.Fatalf("project folder mistaken for the session dir: %q", got)
	}
}
