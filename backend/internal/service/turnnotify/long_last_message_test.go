package turnnotify

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A worker's last message used to reach the orchestrator cut at 500 characters.
func TestMessageKeepsLongLastMessageUpToTheExcerptCap(t *testing.T) {
	rec := domain.SessionRecord{ID: "w1", Metadata: domain.SessionMetadata{Branch: "tp/w-1/fix"}}
	last := strings.Repeat("a", 600)
	if got := Message(KindTurnEnded, rec, last); !strings.Contains(got, last) {
		t.Fatalf("a %d-character last message was cut: %q", len(last), got)
	}
}

func TestMessageMarksATruncatedLastMessage(t *testing.T) {
	rec := domain.SessionRecord{ID: "w1", Metadata: domain.SessionMetadata{Branch: "tp/w-1/fix"}}
	for name, last := range map[string]string{
		"cut by the stored excerpt cap": strings.Repeat("a", domain.TurnFallbackExcerptCharacters-1) + "…",
		"cut by the report limit":       strings.Repeat("a", domain.TurnFallbackExcerptCharacters),
	} {
		got := Message(KindTurnEnded, rec, last)
		if !strings.HasSuffix(got, "[truncated, full: tallpine session history w1]") || len([]rune(got)) > domain.MaxReportTextCharacters {
			t.Errorf("%s: no pointer to the full text or over the limit (%d): %q", name, len([]rune(got)), got[len(got)-80:])
		}
	}
	if got := Message(KindTurnEnded, rec, "short answer"); strings.Contains(got, "truncated") {
		t.Errorf("a whole message was marked truncated: %q", got)
	}
}
