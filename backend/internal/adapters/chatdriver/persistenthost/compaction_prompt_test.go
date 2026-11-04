package persistenthost

import "testing"

func TestIsCompactionPromptAcceptsInstructions(t *testing.T) {
	for params, want := range map[string]bool{
		`{"prompt":[{"type":"text","text":"/compact"}]}`:                   true,
		`{"prompt":[{"type":"text","text":"/compact Keep the PR table"}]}`: true,
		`{"prompt":[{"type":"text","text":"/compactly said"}]}`:            false,
		`{"prompt":[{"type":"text","text":"hello"}]}`:                      false,
	} {
		if got := isCompactionPrompt([]byte(params)); got != want {
			t.Errorf("isCompactionPrompt(%s) = %v, want %v", params, got, want)
		}
	}
}
