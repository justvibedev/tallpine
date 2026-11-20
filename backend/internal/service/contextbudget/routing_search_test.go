package contextbudget

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Q45 and Q46 of the owner's Waiting strip: a conflict-marker regex in 9
// sessions and the bare word `export` in 3.
func TestQ45ConflictMarkerRegexAndQ46ExportAreNoGaps(t *testing.T) {
	var q45, q46 []store.ToolActivity
	for i := 1; i <= 9; i++ {
		q45 = huntRead(append(q45, cmd("s"+string(rune('0'+i)), `grep -rn '^<<<<<<<\|^=======\|^>>>>>>>' .`)), "s"+string(rune('0'+i)))
	}
	for _, s := range []string{"s1", "s2", "s3"} {
		q46 = huntRead(append(q46, cmd(s, "grep -rn export .")), s)
	}
	if got, _ := routing(docFiles(), q45...); len(got) != 0 {
		t.Fatalf("Q45 conflict markers became a gap: %+v", got)
	}
	if got, _ := routing(docFiles(), q46...); len(got) != 0 {
		t.Fatalf("Q46 export became a gap: %+v", got)
	}
	// The same 9 sessions on a concept lookup do qualify: the rows are not what excluded them.
	var concept []store.ToolActivity
	for i := 1; i <= 9; i++ {
		concept = huntRead(append(concept, cmd("s"+string(rune('0'+i)), "grep -rn 'deploy runbook' .")), "s"+string(rune('0'+i)))
	}
	if got, _ := routing(docFiles(), concept...); len(got) != 1 {
		t.Fatalf("control concept lookup gave %+v, want one", got)
	}
}

func TestSearchNeedsMostSessionsToEndInOneDocAndRealSavedCalls(t *testing.T) {
	files := docFiles()
	files["docs/other.md"] = "# Other\n"
	split := func(inOther int) []store.ToolActivity {
		var rows []store.ToolActivity
		for i := range 10 {
			d := "docs/deploy.md"
			if i < inOther {
				d = "docs/other.md"
			}
			s := "s" + string(rune('a'+i))
			rows = append(rows, read(s, d), cmd(s, "ls docs"), cmd(s, "rg 'deploy runbook' ."))
		}
		return rows
	}
	hasSearchGap := func(gs []gap) bool {
		for _, g := range gs {
			if strings.HasPrefix(g.Fingerprint, "routing_pointer:search:") {
				return true
			}
		}
		return false
	}
	if got, _ := routing(files, split(6)...); !hasSearchGap(got) {
		t.Fatalf("6 of 10 (60%%) ending in one doc should give a search gap: %+v", got)
	}
	if got, _ := routing(files, split(5)...); hasSearchGap(got) {
		t.Fatalf("a 5/5 split between two docs gave a search gap: %+v", got)
	}
	// Search straight into the doc: nothing to save.
	var direct []store.ToolActivity
	for _, s := range fiveSessions {
		direct = append(direct, read(s, "docs/deploy.md"), cmd(s, "rg 'deploy runbook' ."))
	}
	if got, _ := routing(files, direct...); hasSearchGap(got) {
		t.Fatalf("a search that cost two calls gave %+v, want the saved-calls bar", got)
	}
}

func TestSearchGapForADocAlreadyLinkedIsSkipped(t *testing.T) {
	files := docFiles()
	files["AGENTS.md"] = agentsMD + "- Deploy: see `docs/deploy.md`.\n"
	var rows []store.ToolActivity
	for _, s := range fiveSessions {
		rows = append(rows, cmd(s, "rg 'deploy runbook' ."))
	}
	if got, _ := routing(files, huntRead(rows, fiveSessions...)...); len(got) != 0 {
		t.Fatalf("a doc AGENTS.md links gave %+v", got)
	}
}
