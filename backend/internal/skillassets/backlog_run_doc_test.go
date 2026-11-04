package skillassets

import (
	"strings"
	"testing"
)

// The orchestrator's run-mode behaviour lives only in this guide, so the rules
// the owner depends on must stay in it.
func TestBacklogGuideKeepsRunModeRules(t *testing.T) {
	body, err := files.ReadFile("using-tallpine/commands/backlog.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(string(body)), " ")
	for _, want := range []string{
		"## Run mode", "TALLPINE_BACKLOG_RUN=off", "Plan first, then run", "You write the item status", "frees the worker slot", "Re-planning keeps work on record", "one-PR children", "Same area, one at a time", "plan_pending", "Plans come first when planning is on", "No stacked PRs",
		"At most 3 workers", "independent Opus review", "native review", "Run local CI before every merge", "5-hour limit",
		"One specific question", "Fails twice, hold", "start nothing new", "run status", "what landed, what waits on the owner, what is held",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("backlog guide lost run-mode rule %q", want)
		}
	}
}
