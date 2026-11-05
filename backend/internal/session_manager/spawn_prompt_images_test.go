package sessionmanager

import (
	"strings"
	"testing"
)

// A chat agent asked for screenshots told the owner it could not attach images
// and listed raw paths, because the always-on guidance named render only for
// charts, tables, diagrams and mockups. The chat pointer must route image
// requests to render; a terminal session has no render tools, so its pointer
// must not.
func TestTallpineSkillPointer_ChatRoutesScreenshotsToRender(t *testing.T) {
	m := &Manager{dataDir: t.TempDir()}
	const want = "To show screenshots or local images, render them by absolute path (`<img src=\"/abs/shot.png\">`); never claim you cannot show images."
	if chat := m.tallpineSkillPointer(true); !strings.Contains(chat, want) {
		t.Fatalf("chat pointer missing screenshot guidance %q:\n%s", want, chat)
	}
	if terminal := m.tallpineSkillPointer(false); strings.Contains(terminal, "screenshots") {
		t.Fatalf("terminal pointer names chat-only image rendering:\n%s", terminal)
	}
}
