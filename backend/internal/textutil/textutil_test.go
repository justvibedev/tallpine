package textutil

import "testing"

func TestFirstNonBlankKeepsOriginal(t *testing.T) {
	if got := FirstNonBlank("", "  ", " x ", "y"); got != " x " {
		t.Fatalf("got %q", got)
	}
	if got := FirstNonBlank(" ", ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestFirstTrimmedTrims(t *testing.T) {
	if got := FirstTrimmed("", "  ", " x ", "y"); got != "x" {
		t.Fatalf("got %q", got)
	}
}

func TestHeadBytesCutsOnRuneBoundary(t *testing.T) {
	if got, cut := HeadBytes("abc", 5); got != "abc" || cut {
		t.Fatalf("short = %q %v", got, cut)
	}
	// "é" is two bytes; a limit inside it backs up to the rune start.
	if got, cut := HeadBytes("aé", 2); got != "a" || !cut {
		t.Fatalf("mid-rune = %q %v", got, cut)
	}
	if got, cut := HeadBytes("abcdef", 3); got != "abc" || !cut {
		t.Fatalf("ascii = %q %v", got, cut)
	}
}
