package buildinfo

import "testing"

func TestString(t *testing.T) {
	saved := [4]string{Version, Commit, Date, Dirty}
	t.Cleanup(func() { Version, Commit, Date, Dirty = saved[0], saved[1], saved[2], saved[3] })

	Version, Commit, Date, Dirty = "dev", "", "", ""
	if got := String(); got != "dev" {
		t.Fatalf("String() = %q, want dev", got)
	}
	Version, Commit, Date, Dirty = "0.13.0", "abc123", "2026-10-26T10:00:00Z", "true"
	if got, want := String(), "0.13.0 commit abc123 built 2026-10-26T10:00:00Z (dirty)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	Dirty = "false"
	if got, want := String(), "0.13.0 commit abc123 built 2026-10-26T10:00:00Z"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
	if f := Fields(); f["commit"] != "abc123" || f["dirty"] != false || f["buildDate"] != Date {
		t.Fatalf("Fields() = %v", f)
	}
}
