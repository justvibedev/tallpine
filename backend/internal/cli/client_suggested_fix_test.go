package cli

import "testing"

func TestAPIErrorStringIncludesSuggestedFix(t *testing.T) {
	e := apiError{Message: "bad", Code: "X", Details: map[string]any{"suggestedFix": " Do the thing. "}}
	if got, want := e.String(), "bad (X)\nSuggested fix: Do the thing."; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := (apiError{Message: "bad", Details: map[string]any{"path": "/x"}}).String(); got != "bad" {
		t.Fatalf("got %q", got)
	}
}
