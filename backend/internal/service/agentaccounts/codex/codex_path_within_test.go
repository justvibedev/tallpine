package codex

import "testing"

func TestPathWithinIsStrictContainment(t *testing.T) {
	for _, tc := range []struct {
		name, root, candidate string
		want                  bool
	}{
		{"child", "/a/b", "/a/b/c", true},
		{"root itself", "/a/b", "/a/b", false},
		{"sibling sharing a prefix", "/a/b", "/a/bc", false},
		{"parent traversal", "/a/b", "/a/b/../c", false},
		{"empty root", "", "/a/b", false},
		{"empty candidate", "/a/b", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathWithin(tc.root, tc.candidate); got != tc.want {
				t.Fatalf("pathWithin(%q, %q) = %t, want %t", tc.root, tc.candidate, got, tc.want)
			}
		})
	}
}
