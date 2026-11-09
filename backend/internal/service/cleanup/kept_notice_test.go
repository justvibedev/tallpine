package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func collectNotices(rule *WorktreeRule) *[]KeptNotice {
	var got []KeptNotice
	rule.SetKeptNotifier(func(_ context.Context, n KeptNotice) { got = append(got, n) })
	return &got
}

func TestWorktreeRuleNotifiesWhenItKeepsADirtyWorktreeWithAnOpenPR(t *testing.T) {
	f := newWorktreeFixture(t)
	dirtyWorktree(t, f, "open")
	salvager := NewSalvager(t.TempDir(), func(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
		return []domain.PullRequest{{URL: "u"}}, nil
	})
	rule := NewWorktreeRule(f.root, f.remover, nil)
	rule.SetSalvager(salvager)
	got := collectNotices(rule)
	item := itemFor(t, f.runWith(rule, RunRequest{}), "open")
	if len(*got) != 1 || (*got)[0].SessionID != "open" || (*got)[0].ReasonKey != "uncommitted changes" || !strings.Contains((*got)[0].Reason, "still open") {
		t.Fatalf("notices = %+v, want one for the open-PR keep", *got)
	}
	if !item.Attention {
		t.Fatalf("item = %+v, want Attention", item)
	}
}

func TestWorktreeRuleNamesTheSalvagePathWhenSalvageIsPartial(t *testing.T) {
	f := newWorktreeFixture(t)
	dirtyWorktree(t, f, "part")
	salvager := NewSalvager(t.TempDir(), nil)
	// An earlier snapshot exists; the current attempt fails on size.
	prev := filepath.Join(salvager.Root(), "part", "20260101T000000Z", "workspace")
	if err := os.MkdirAll(prev, 0o700); err != nil {
		t.Fatal(err)
	}
	old := salvageMaxTotal
	salvageMaxTotal = 4
	t.Cleanup(func() { salvageMaxTotal = old })
	rule := NewWorktreeRule(f.root, f.remover, nil)
	rule.SetSalvager(salvager)
	got := collectNotices(rule)
	item := itemFor(t, f.runWith(rule, RunRequest{}), "part")
	want := filepath.Join(salvager.Root(), "part")
	if len(*got) != 1 || (*got)[0].SalvagePath != want || !strings.Contains(item.Reason, "salvaged work: "+want) {
		t.Fatalf("notices = %+v reason = %q, want the salvage path %s", *got, item.Reason, want)
	}
	if (*got)[0].ReasonKey != "its work could not be salvaged" {
		t.Fatalf("key = %q", (*got)[0].ReasonKey)
	}
}

func TestWorktreeRuleDoesNotNotifyForRetentionOrDryRun(t *testing.T) {
	f := newWorktreeFixture(t)
	f.session("young", true, time.Hour, "")
	f.session("dirty", true, 30*24*time.Hour, "")
	f.keep[f.paths["dirty"]] = "uncommitted changes"
	rule := NewWorktreeRule(f.root, f.remover, func(_ context.Context, path string, _ bool) (string, error) { return f.keep[path], nil })
	got := collectNotices(rule)
	report := f.runWith(rule, RunRequest{DryRun: true})
	if len(*got) != 0 {
		t.Fatalf("dry run notified: %+v", *got)
	}
	if !itemFor(t, report, "dirty").Attention || itemFor(t, report, "young").Attention {
		t.Fatal("only the dirty worktree needs attention")
	}
	f.runWith(rule, RunRequest{})
	if len(*got) != 1 || (*got)[0].SessionID != "dirty" {
		t.Fatalf("notices = %+v, want only the dirty worktree (not the one inside retention)", *got)
	}
}

func TestKeptReasonKeyIsStableAcrossDetail(t *testing.T) {
	cases := map[string]string{
		"uncommitted changes in sub/a":                                       "uncommitted changes",
		"uncommitted changes; not salvaged: its pull request is still open":  "uncommitted changes",
		"its work could not be salvaged: untracked nested git repository x/": "nested-repo",
		"its work could not be salvaged: untracked nested git repository y/": "nested-repo",
		"its work could not be salvaged: still dirty after salvage: M a":     "dirty-after-salvage",
		"commits that are not merged or pushed":                              "commits that are not merged or pushed",
		"ended less than 7 day(s) ago":                                       "",
		"in use by a live session":                                           "",
		"session is live":                                                    "",
		"session not found":                                                  "",
		"session was restored":                                               "",
		"workspace cleanup is already running":                               "",
		"workspace in use by a live session":                                 "",
		"worktree is in use; will retry on a later run":                      "",
	}
	for reason, want := range cases {
		if got := keptReasonKey(reason); got != want {
			t.Errorf("keptReasonKey(%q) = %q, want %q", reason, got, want)
		}
	}
}

func writeSnapshot(t *testing.T, s *Salvager, rel string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(s.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func pruneOnce(s *Salvager) {
	s.pruneSalvage(context.Background(), &Run{Now: time.Now(), report: &CategoryReport{}})
}

func TestPruneSalvageRemovesOnlyProvableDuplicates(t *testing.T) {
	s := NewSalvager(t.TempDir(), nil)
	same := map[string]string{"workspace/changes.patch": "P", "README.md": "stamp-specific"}
	dup1 := writeSnapshot(t, s, "s1/20260101T010000Z", same)
	dup2 := writeSnapshot(t, s, "s1/20260101T020000Z", map[string]string{"workspace/changes.patch": "P", "README.md": "other"})
	newest := writeSnapshot(t, s, "s1/20260101T030000Z", same)
	other := writeSnapshot(t, s, "s2/20260101T010000Z", same)
	pruneOnce(s)
	if exists(dup1) || exists(dup2) || !exists(newest) || !exists(other) {
		t.Fatalf("dup1=%v dup2=%v newest=%v other=%v, want only the identical older snapshots removed", exists(dup1), exists(dup2), exists(newest), exists(other))
	}
}

// A newer snapshot of different work never makes an older one redundant: after
// B537 the worktree is reset, so the older snapshot can be the only copy.
func TestPruneSalvageKeepsAnOlderSnapshotWithDifferentWork(t *testing.T) {
	s := NewSalvager(t.TempDir(), nil)
	older := writeSnapshot(t, s, "s1/20260101T010000Z", map[string]string{"workspace/changes.patch": "REAL", "workspace/untracked.tar.gz": "U"})
	multi := writeSnapshot(t, s, "s2/20260101T010000Z", map[string]string{"a/changes.patch": "A", "b/changes.patch": "B"})
	writeSnapshot(t, s, "s1/20260101T020000Z", map[string]string{"workspace/changes.patch": "remainder"})
	writeSnapshot(t, s, "s2/20260101T020000Z", map[string]string{"b/changes.patch": "B"})
	pruneOnce(s)
	if !exists(older) || !exists(multi) {
		t.Fatalf("older=%v multi=%v, want both kept for the age-out", exists(older), exists(multi))
	}
}

// A crash mid-salvage leaves only a dot-prefixed temp directory in its stamp.
func TestPruneSalvageIgnoresALeftoverTempDirectory(t *testing.T) {
	s := NewSalvager(t.TempDir(), nil)
	complete := writeSnapshot(t, s, "s1/20260101T010000Z", map[string]string{"workspace/changes.patch": "P"})
	writeSnapshot(t, s, "s1/20260101T020000Z", map[string]string{".workspace-123/changes.patch": "partial"})
	pruneOnce(s)
	if !exists(complete) {
		t.Fatal("a complete snapshot was removed as superseded by a partial temp directory")
	}
	if got := s.SessionDir("s1"); got == "" {
		t.Fatal("the complete snapshot should still count as salvaged work")
	}
	only := NewSalvager(t.TempDir(), nil)
	writeSnapshot(t, only, "s9/20260101T020000Z", map[string]string{".workspace-1/x": "partial"})
	if got := only.SessionDir("s9"); got != "" {
		t.Fatalf("SessionDir = %q, want none for a temp-only stamp", got)
	}
}

// The status block must survive the light passes that follow a full pass's
// refusal (24h backoff), and the 50-item cap.
func TestKeptWorktreeStaysFlaggedOnLightPassesAfterAFullPass(t *testing.T) {
	f := newWorktreeFixture(t)
	f.session("dirty", true, 30*24*time.Hour, "")
	rule := NewWorktreeRule(f.root, f.remover, func(context.Context, string, bool) (string, error) { return "uncommitted changes", nil })
	got := collectNotices(rule)
	if item := itemFor(t, f.runWith(rule, RunRequest{Pass: PassFull}), "dirty"); !item.Attention {
		t.Fatalf("full pass item = %+v", item)
	}
	item := itemFor(t, f.runWith(rule, RunRequest{Pass: PassLight}), "dirty")
	if !item.Attention || item.Reason != "uncommitted changes" {
		t.Fatalf("light pass item = %+v, want the original reason, flagged", item)
	}
	if len(*got) != 1 {
		t.Fatalf("notices = %d, want 1 (the backed-off pass sends none)", len(*got))
	}
}

func TestAttentionItemsSurviveTheItemCap(t *testing.T) {
	c := &CategoryReport{}
	for i := 0; i < maxItemsPerCategory+5; i++ {
		c.add(Item{What: "salvaged work", Status: StatusFreed, Bytes: 1 << 20})
	}
	c.add(Item{What: "worktree", Status: StatusKept, Attention: true})
	c.finish()
	if !c.ItemsTruncated || c.Items[0].What != "worktree" {
		t.Fatalf("first item = %+v truncated=%v, want the attention item first", c.Items[0], c.ItemsTruncated)
	}
}
