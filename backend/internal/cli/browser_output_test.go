package cli

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestBrowserPrintsChromeModeHints(t *testing.T) {
	for _, test := range []struct {
		action string
		result map[string]any
		want   string
	}{
		{"devtools-open", map[string]any{"open": false, "hint": "press Cmd+Opt+I in Chrome"}, "Browser devtools-open completed. press Cmd+Opt+I in Chrome\n"},
		{"devtools-close", map[string]any{"open": false}, "Browser devtools-close completed.\n"},
		{"wait", map[string]any{"hint": "page text"}, "Browser wait completed.\n"},
		{"open", map[string]any{"url": "https://accounts.google.com/v3/signin/rejected", "signInHint": "click Sign in to sites"}, "<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>\nhttps://accounts.google.com/v3/signin/rejected\n<<<END UNTRUSTED EXTERNAL CONTENT>>>\nclick Sign in to sites\n"},
		{"click", map[string]any{"signInHint": "click Sign in to sites"}, "Browser click completed.\nclick Sign in to sites\n"},
		{"snapshot", map[string]any{"text": "page", "signInHint": "click Sign in to sites"}, "<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>\npage\n<<<END UNTRUSTED EXTERNAL CONTENT>>>\nclick Sign in to sites\n"},
		{"press", map[string]any{"target": map[string]any{"tabId": "T1"}}, "Browser press completed.\n"},
		{"open", map[string]any{"url": "https://example.com/"}, "<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>\nhttps://example.com/\n<<<END UNTRUSTED EXTERNAL CONTENT>>>\n"},
		{"wait", map[string]any{"url": "https://example.com/", "hint": "page text"}, "<<<BEGIN UNTRUSTED EXTERNAL CONTENT>>>\nhttps://example.com/\n<<<END UNTRUSTED EXTERNAL CONTENT>>>\n"},
	} {
		var output bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&output)
		if err := writeBrowserResult(cmd, test.action, test.result); err != nil {
			t.Fatal(err)
		}
		if output.String() != test.want {
			t.Fatalf("%s output = %q, want %q", test.action, output.String(), test.want)
		}
	}
}
