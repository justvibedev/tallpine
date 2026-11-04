package cleanup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func goCacheFixture(t *testing.T) (dir string, oldFile, freshFile string) {
	t.Helper()
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "README"), 0, time.Time{})
	if err := writeGoCacheReadme(filepath.Join(dir, "README")); err != nil {
		t.Fatal(err)
	}
	oldFile = filepath.Join(dir, "ab", "old-d")
	freshFile = filepath.Join(dir, "cd", "fresh-d")
	writeFile(t, oldFile, 1000, scratchNow.Add(-3*24*time.Hour))
	writeFile(t, freshFile, 500, scratchNow.Add(-time.Hour))
	return dir, oldFile, freshFile
}

func TestGoBuildCacheRuleTrimsEntriesUnusedForTwoDays(t *testing.T) {
	dir, oldFile, freshFile := goCacheFixture(t)
	store := &fakeStore{policy: `{"goBuildCache":{"enabled":true}}`}
	rule := NewGoBuildCacheRule(dir)

	if dry := runRules(t, store, RunRequest{DryRun: true}, rule); dry.Categories[0].WouldFreeBytes != 1000 || !exists(oldFile) {
		t.Fatalf("dry run: would free %d, old kept=%t", dry.Categories[0].WouldFreeBytes, exists(oldFile))
	}
	report := runRules(t, store, RunRequest{}, rule)
	if report.FreedBytes != 1000 || exists(oldFile) || !exists(freshFile) || !exists(filepath.Join(dir, "README")) {
		t.Fatalf("freed %d; old=%t fresh=%t", report.FreedBytes, exists(oldFile), exists(freshFile))
	}
}

// The cache is the user's own, outside the data dir: a default policy only
// reports.
func TestGoBuildCacheRuleOnlyReportsByDefault(t *testing.T) {
	dir, oldFile, _ := goCacheFixture(t)
	report := runRules(t, &fakeStore{policy: "{}"}, RunRequest{}, NewGoBuildCacheRule(dir))
	if !exists(oldFile) || report.FreedBytes != 0 || report.Categories[0].WouldFreeBytes != 1000 {
		t.Fatalf("default policy: old kept=%t freed=%d would=%d", exists(oldFile), report.FreedBytes, report.Categories[0].WouldFreeBytes)
	}
}

func TestGoBuildCacheRuleIgnoresADirectoryThatIsNotAGoCache(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "ab", "important")
	writeFile(t, victim, 100, scratchNow.Add(-90*24*time.Hour))
	runRules(t, &fakeStore{policy: `{"goBuildCache":{"enabled":true}}`}, RunRequest{}, NewGoBuildCacheRule(dir))
	if !exists(victim) {
		t.Fatal("removed a file from a directory without Go's README marker")
	}
}

func writeGoCacheReadme(path string) error {
	return os.WriteFile(path, []byte(goCacheMarker+". Safe to delete.\n"), 0o600)
}
