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
