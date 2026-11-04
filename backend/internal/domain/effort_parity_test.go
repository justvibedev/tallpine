package domain

import (
	"os"
	"reflect"
	"regexp"
	"testing"
)

func quotedList(t *testing.T, src, declaration string) []string {
	t.Helper()
	m := regexp.MustCompile(declaration).FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("%q not found in frontend/src/shared/effort.ts", declaration)
	}
	var out []string
	for _, q := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(m[1], -1) {
		out = append(out, q[1])
	}
	return out
}

func TestEffortOrderMatchesFrontend(t *testing.T) {
	raw, err := os.ReadFile("../../../frontend/src/shared/effort.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if got := quotedList(t, src, `EFFORT_ORDER: [^=]*= \[([^\]]*)\]`); !reflect.DeepEqual(got, EffortOrder) {
		t.Errorf("frontend EFFORT_ORDER %v != domain.EffortOrder %v", got, EffortOrder)
	}
	for agent, want := range AgentEfforts {
		got := quotedList(t, src, `"?`+regexp.QuoteMeta(agent)+`"?: \[([^\]]*)\]`)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("frontend AGENT_EFFORTS[%s] %v != domain %v", agent, got, want)
		}
	}
}

func TestEffortRankAndLowest(t *testing.T) {
	for _, c := range []struct {
		level string
		rank  int
		ok    bool
	}{{"none", 0, true}, {"OFF", 0, true}, {"minimal", 1, true}, {"low", 2, true}, {"ultra", 7, true}, {"default", 0, false}} {
		if r, ok := EffortRank(c.level); r != c.rank || ok != c.ok {
			t.Errorf("EffortRank(%q) = %d,%v", c.level, r, ok)
		}
	}
	if got := LowestEffort([]string{"high", "ultra", "medium", "default"}); got != "medium" {
		t.Errorf("LowestEffort = %q", got)
	}
	if got := LowestEffort([]string{"ultra", "max"}); got != "max" {
		t.Errorf("ultra must sort above max, got %q", got)
	}
	if got := LowestEffort([]string{"default"}); got != "" {
		t.Errorf("unknown only = %q", got)
	}
}
