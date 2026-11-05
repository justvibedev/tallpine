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

// B559: an orchestrator on Claude Code had html_render only as a deferred tool
// (a name, no schema), never loaded it, and answered "send me screenshots" with
// local paths the owner could not open on the phone. The chat pointer must say
// how to load deferred html tools, and must tie an image request to html_render
// rather than a list of paths.
func TestTallpineSkillPointer_ChatLoadsDeferredToolsForImageRequests(t *testing.T) {
	m := &Manager{dataDir: t.TempDir()}
	chat := m.tallpineSkillPointer(true)
	for _, want := range []string{
		"If they are listed only as deferred tools, load them first",
		"ToolSearch `select:mcp__tallpine__html_preview,mcp__tallpine__html_render`",
		"When asked for screenshots or images, call `html_render` with one `<img>` per image",
		"Never reply with only local paths: the phone cannot open them.",
	} {
		if !strings.Contains(chat, want) {
			t.Fatalf("chat pointer missing %q:\n%s", want, chat)
		}
	}
	terminal := m.tallpineSkillPointer(false)
	for _, banned := range []string{"deferred tools", "html_render"} {
		if strings.Contains(terminal, banned) {
			t.Fatalf("terminal pointer names chat-only %q:\n%s", banned, terminal)
		}
	}
}

// The chat pointer keeps its render routing and stays a short pointer, with
// the details in routed command guides.
func TestTallpineSkillPointer_ChatRenderRoutingAndBudget(t *testing.T) {
	chatPointer := (&Manager{dataDir: t.TempDir()}).tallpineSkillPointer(true)
	for _, want := range []string{
		"When a chart, table, diagram, or mockup is clearer than text, call `html_preview`, then `html_render`. If you cannot see them, search your tools for them. ",
		"If you find nothing, read `",
		"` and use `tallpine render`. Do not use a built-in visualize skill.",
	} {
		if !strings.Contains(chatPointer, want) {
			t.Fatalf("chat pointer missing %q:\n%s", want, chatPointer)
		}
	}
	// B559 added the deferred-tool load and the image-request rule (~45 words);
	// agents that lacked them told the owner they could not show images.
	if words := len(strings.Fields(chatPointer)); words > 330 {
		t.Fatalf("always-on Tallpine skill pointer grew to %d words; keep details in routed command guides:\n%s", words, chatPointer)
	}
}
