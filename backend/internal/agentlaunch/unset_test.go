package agentlaunch

import (
	"slices"
	"testing"
)

func TestUnsetNamesValidatesAndFilterDropsOnlyThose(t *testing.T) {
	env := map[string]string{EnvUnset: "GH_TOKEN, GITHUB_TOKEN,bad name,1X,;rm", "KEEP": "1"}
	got := UnsetNames(env)
	if !slices.Equal(got, []string{"GH_TOKEN", "GITHUB_TOKEN"}) {
		t.Fatalf("only valid names: %v", got)
	}
	environ := []string{"GH_TOKEN=a", "GITHUB_TOKEN=b", "PATH=/bin", "KEEP=1"}
	if out := FilterUnset(environ, got); !slices.Equal(out, []string{"PATH=/bin", "KEEP=1"}) {
		t.Fatalf("%v", out)
	}
	if out := FilterUnset(environ, nil); len(out) != 4 {
		t.Fatal("no names: environ unchanged")
	}
	m := map[string]string{}
	SetUnset(m, nil)
	if _, ok := m[EnvUnset]; ok {
		t.Fatal("an empty list must not set the key")
	}
	SetUnset(m, []string{"A", "B"})
	if m[EnvUnset] != "A,B" {
		t.Fatalf("%v", m)
	}
}

func TestScrubbedAndControlKeys(t *testing.T) {
	if Scrubbed(nil) || Scrubbed(map[string]string{EnvScrub: "0"}) || !Scrubbed(map[string]string{EnvScrub: "1"}) {
		t.Fatal("only 1 scrubs")
	}
	in := map[string]string{EnvScrub: "1", EnvUnset: "A", "K": "v"}
	out := WithoutControlKeys(in)
	if len(out) != 1 || out["K"] != "v" || len(in) != 3 {
		t.Fatalf("control keys dropped without mutating the input: %v %v", out, in)
	}
	plain := map[string]string{"K": "v"}
	if got := WithoutControlKeys(plain); len(got) != 1 {
		t.Fatal(got)
	}
}
