package usage

import (
	"context"
	"path/filepath"
	"testing"
)

// withinDesiredRoot is inclusive: a watched root counts as inside itself.
func TestWithinDesiredRootIsInclusive(t *testing.T) {
	root := t.TempDir()
	w := &TranscriptWatcher{roots: []string{root}}
	ctx := context.Background()
	if !w.withinDesiredRoot(ctx, root) {
		t.Fatal("root itself should count")
	}
	if !w.withinDesiredRoot(ctx, filepath.Join(root, "x", "y.jsonl")) {
		t.Fatal("child should count")
	}
	if w.withinDesiredRoot(ctx, root+"-sibling") {
		t.Fatal("prefix sibling should not count")
	}
}
