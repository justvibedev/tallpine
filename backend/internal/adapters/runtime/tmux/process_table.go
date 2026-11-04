package tmux

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

type processEntry struct {
	pid     int
	ppid    int
	command string
}

func parseProcessTable(out string) ([]processEntry, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	lines := strings.Split(trimmed, "\n")
	entries := make([]processEntry, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("incomplete process row %q", line)
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("invalid pid in %q", line)
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("invalid parent pid in %q", line)
		}
		entries = append(entries, processEntry{pid: pid, ppid: ppid, command: strings.Join(fields[2:], " ")})
	}
	return entries, nil
}

func descendantPIDs(entries []processEntry, rootPID int) map[int]bool {
	descendants := map[int]bool{rootPID: true}
	for changed := true; changed; {
		changed = false
		for _, entry := range entries {
			if descendants[entry.pid] || !descendants[entry.ppid] {
				continue
			}
			descendants[entry.pid] = true
			changed = true
		}
	}
	return descendants
}

func containsManagedWorkload(entries []processEntry, rootPID int, sessionID, launchID string) bool {
	descendants := descendantPIDs(entries, rootPID)
	hasChild := false
	hasSupervisor := false
	for _, entry := range entries {
		if entry.pid == rootPID || !descendants[entry.pid] {
			continue
		}
		hasChild = true
		if !isAnySupervisorCommand(entry.command) {
			continue
		}
		hasSupervisor = true
		if isSupervisorCommand(entry.command, sessionID, launchID) {
			return true
		}
	}

	// A supervisor in the pane tree must match the current generation. Once no
	// supervisor remains, the pane root is the preserved interactive shell and
	// any child is a workload the operator launched from that shell.
	return hasChild && !hasSupervisor
}

func containsUnsupervisedReviewerWorkload(entries []processEntry, rootPID int) bool {
	descendants := descendantPIDs(entries, rootPID)
	for _, entry := range entries {
		if entry.pid == rootPID || !descendants[entry.pid] || isAnySupervisorCommand(entry.command) {
			continue
		}
		if isPreservedPaneProcess(entry.command) {
			continue
		}
		return true
	}
	return false
}

func isPreservedPaneProcess(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return true
	}
	name := filepath.Base(fields[0])
	switch name {
	case "sh", "bash", "zsh", "fish", "ksh", "dash", "nu", "cat":
		return true
	default:
		return false
	}
}

func containsExactSupervisedWorkload(entries []processEntry, rootPID int, sessionID, launchID string) bool {
	descendants := descendantPIDs(entries, rootPID)
	supervisorPID := 0
	for _, entry := range entries {
		if entry.pid != rootPID && descendants[entry.pid] && isSupervisorCommand(entry.command, sessionID, launchID) {
			supervisorPID = entry.pid
			break
		}
	}
	if supervisorPID == 0 {
		return false
	}
	workloadDescendants := descendantPIDs(entries, supervisorPID)
	for _, entry := range entries {
		if entry.pid != supervisorPID && workloadDescendants[entry.pid] {
			return true
		}
	}
	return false
}

func isAnySupervisorCommand(command string) bool {
	fields := strings.Fields(command)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "agent-process" && fields[i+1] == "supervise" {
			return true
		}
	}
	return false
}

func isSupervisorCommand(command, sessionID, launchID string) bool {
	fields := strings.Fields(command)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] != "agent-process" || fields[i+1] != "supervise" {
			continue
		}
		gotSession, gotLaunch := "", ""
		for j := i + 2; j+1 < len(fields) && fields[j] != "--"; j++ {
			switch fields[j] {
			case "--session":
				gotSession = fields[j+1]
				j++
			case "--launch":
				gotLaunch = fields[j+1]
				j++
			}
		}
		if gotSession == sessionID && gotLaunch == launchID {
			return true
		}
	}
	return false
}

// -- session name helpers --
