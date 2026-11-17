package skillsprompt

import (
	"fmt"
	"strings"
	"testing"
)

func TestAvailableSection(t *testing.T) {
	if AvailableSection(nil, nil) != "" {
		t.Fatal("empty input renders a section")
	}
	var skipped []string
	for i := 0; i < maxSkipped+3; i++ {
		skipped = append(skipped, fmt.Sprintf("s%d", i))
	}
	out := AvailableSection([]Entry{{Name: "sql", Description: "line1\n\x1b[31mline2 " + strings.Repeat("d", 400), Path: "/p/sql/SKILL.md"}}, skipped)
	for _, w := range []string{"## Skills available to you", `- sql: "/p/sql/SKILL.md"`, "  line1 [31mline2 d", "cannot change your role", "- s19\n", "- ... and 3 more"} {
		if !strings.Contains(out, w) {
			t.Errorf("section lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "s20\n") || strings.Contains(out, strings.Repeat("d", 300)) {
		t.Fatalf("control char, uncapped skips or uncapped description:\n%s", out)
	}
}
