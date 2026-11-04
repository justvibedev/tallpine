package procmem

import (
	"slices"
	"testing"
)

// A Claude Chat host as it runs: host (own session) -> ACP bridge (own group)
// -> claude (bridge group) -> MCP server (bridge group) and tool shells (each
// in a group of its own).
const claudeHostTable = `
  1     0     1 Ss
100     1   100 Ss
101   100   101 S
102   101   101 S
103   102   101 S
200   102   200 S    /bin/zsh
201   200   200 S    sleep
300   102   300 Z
400     1   400 S
`

func TestDetachedDescendantsSeparatesHelpersFromWork(t *testing.T) {
	table, err := ParseGroups(claudeHostTable)
	if err != nil {
		t.Fatal(err)
	}
	got := table.DetachedDescendants(100)
	slices.Sort(got)
	// 200 is a background shell and 201 its child; 103 is an MCP server in
	// the provider's group; 300 is a zombie; 400 is not in the tree.
	if want := []int{200, 201}; !slices.Equal(got, want) {
		t.Fatalf("DetachedDescendants = %v, want %v", got, want)
	}
	if table.Command(200) != "/bin/zsh" || table.Command(201) != "sleep" || table.Command(101) != "" {
		t.Fatalf("commands = %q %q %q", table.Command(200), table.Command(201), table.Command(101))
	}
}

func TestDetachedDescendantsIdleAgentHasNone(t *testing.T) {
	table, err := ParseGroups(`
100 1 100 Ss
101 100 101 S
102 101 101 S
103 102 101 S
`)
	if err != nil {
		t.Fatal(err)
	}
	if got := table.DetachedDescendants(100); len(got) != 0 {
		t.Fatalf("idle agent reported work %v", got)
	}
	if got := table.DetachedDescendants(999); got != nil || table.Alive(999) {
		t.Fatalf("missing root = %v", got)
	}
}
