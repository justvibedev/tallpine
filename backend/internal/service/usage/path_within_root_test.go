package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPathWithinRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "b")
	sibling := filepath.Join(base, "bc")
	outside := filepath.Join(base, "out")
	for _, d := range []string{root, sibling, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inside := filepath.Join(root, "f.jsonl")
	for _, f := range []string{inside, filepath.Join(sibling, "f.jsonl"), filepath.Join(outside, "f.jsonl")} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "escape.jsonl")
	if err := os.Symlink(filepath.Join(outside, "f.jsonl"), link); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name       string
		ctx        context.Context
		path, root string
		want       bool
	}{
		{"inside", context.Background(), inside, root, true},
		{"root itself", context.Background(), root, root, true},
		{"prefix sibling", context.Background(), filepath.Join(sibling, "f.jsonl"), root, false},
		{"symlink escape", context.Background(), link, root, false},
		{"missing path", context.Background(), filepath.Join(root, "nope"), root, false},
		{"missing root", context.Background(), inside, filepath.Join(base, "nope"), false},
		{"blank path", context.Background(), " ", root, false},
		{"blank root", context.Background(), inside, "", false},
		{"canceled", canceled, inside, root, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathWithinRoot(tc.ctx, tc.path, tc.root); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
