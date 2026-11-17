package sessionmanager

import (
	"context"
	"errors"
	"testing"
)

// B730: an explicit --name names the branch, not the prompt body, which may
// mention other tasks or versions.
func TestTaskNamingFallbackUsesExplicitTitle(t *testing.T) {
	t.Parallel()

	m, st, _, _ := newGitTaskPreparationManager(t)
	m.dataDir = t.TempDir()
	m.chat = &taskNamingLauncher{err: errors.New("offline")}
	cfg := asyncChatSpawnConfig("Previous release was 0.13.15; bump the version")
	cfg.NameFromPrompt, cfg.Async, cfg.DisplayName, cfg.KeepDisplayName = true, false, "Release 0.13.17", true
	rec, _, _, err := m.Spawn(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[rec.ID].Metadata.Branch; got != "tp/release-0-13-17/root" {
		t.Fatalf("branch = %q", got)
	}
}

// B739: a model slug that names another version never reaches the branch.
func TestKeepTitleVersions(t *testing.T) {
	t.Parallel()

	title := "Release 0.13.18: version + changelog PR"
	titleSlug := taskSlug(title)
	for _, tc := range []struct {
		slug, want string
		explicit   bool
	}{
		{"release-0-13-16", titleSlug, true},
		{"release-0-13-19", titleSlug, true},
		{"release-notes", titleSlug, true},
		{"release-0-13-18", "release-0-13-18", true},
		{"release-0-13-16", "release-0-13-16", false},
	} {
		if got := keepTitleVersions(title, tc.explicit, tc.slug, titleSlug); got != tc.want {
			t.Errorf("keepTitleVersions(%q, %v) = %q, want %q", tc.slug, tc.explicit, got, tc.want)
		}
	}
	if titleSlug != "release-0-13-18-version" {
		t.Fatalf("titleSlug = %q", titleSlug)
	}
}
