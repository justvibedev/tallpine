package procmem

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// GroupTable is a snapshot of every live process with its process group,
// for telling an agent's helpers from the work it started.
type GroupTable struct {
	pgid     map[int]int
	children map[int][]int
	command  map[int]string
}

// ParseGroups builds a GroupTable from `ps -A -o pid=,ppid=,pgid=,stat=,comm=`
// output; the command column is optional. Zombies have exited and are left out.
func ParseGroups(out string) (*GroupTable, error) {
	t := &GroupTable{pgid: map[int]int{}, children: map[int][]int{}, command: map[int]string{}}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		pgid, err3 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("procmem: malformed ps row %q", sc.Text())
		}
		if len(fields) > 3 && strings.HasPrefix(fields[3], "Z") {
			continue
		}
		t.pgid[pid] = pgid
		if len(fields) > 4 {
			t.command[pid] = strings.Join(fields[4:], " ")
		}
		t.children[ppid] = append(t.children[ppid], pid)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("procmem: scan ps output: %w", err)
	}
	return t, nil
}

// Command is pid's command name, or empty when the snapshot had none.
func (t *GroupTable) Command(pid int) string {
	return t.command[pid]
}

// Alive reports whether pid is in the table.
func (t *GroupTable) Alive(pid int) bool {
	_, ok := t.pgid[pid]
	return ok
}

// DetachedDescendants lists root's descendants that run in a process group
// other than root's own or one led by root's direct children.
//
// A provider host starts its provider in a process group of its own, and the
// agent's long-lived helpers (MCP servers, the bridge) stay in it. A command
// the agent runs for a tool call, background shells included, gets a process
// group of its own, so a descendant outside the helper groups is work the
// agent started and would lose if its group were stopped.
func (t *GroupTable) DetachedDescendants(root int) []int {
	if !t.Alive(root) {
		return nil
	}
	helper := map[int]bool{t.pgid[root]: true}
	for _, child := range t.children[root] {
		helper[t.pgid[child]] = true
	}
	var out []int
	seen := map[int]bool{root: true}
	stack := append([]int(nil), t.children[root]...)
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if !helper[t.pgid[pid]] {
			out = append(out, pid)
		}
		stack = append(stack, t.children[pid]...)
	}
	return out
}
