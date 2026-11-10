//go:build linux

package procmem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

func readParentLink(pid int) (parentLink, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return parentLink{}, ErrProcessGone
	}
	if err != nil {
		return parentLink{}, fmt.Errorf("procmem: read pid %d: %w", pid, err)
	}
	return parseStatLink(string(raw))
}

// parseStatLink reads state, ppid and session from /proc/<pid>/stat. The comm
// field can hold spaces and parentheses, so fields count from its last ')'.
func parseStatLink(stat string) (parentLink, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return parentLink{}, errors.New("procmem: malformed /proc stat")
	}
	fields := strings.Fields(stat[end+1:])
	const stateField, ppidField, sessionField = 0, 1, 3
	if len(fields) <= sessionField {
		return parentLink{}, errors.New("procmem: short /proc stat")
	}
	if fields[stateField] == "Z" || fields[stateField] == "X" {
		return parentLink{}, ErrProcessGone
	}
	ppid, err := strconv.Atoi(fields[ppidField])
	if err != nil {
		return parentLink{}, fmt.Errorf("procmem: ppid: %w", err)
	}
	session, err := strconv.Atoi(fields[sessionField])
	if err != nil {
		return parentLink{}, fmt.Errorf("procmem: session: %w", err)
	}
	return parentLink{ppid: ppid, session: session}, nil
}

// ExecutablePath returns the path pid was executed from, or "" if unreadable.
func ExecutablePath(pid int) string {
	path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
	if err != nil {
		return ""
	}
	return path
}

// ChildPIDs returns pid's direct children, across all of its threads.
func ChildPIDs(pid int) ([]int, error) {
	dir := "/proc/" + strconv.Itoa(pid) + "/task"
	tasks, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrProcessGone
	}
	if err != nil {
		return nil, fmt.Errorf("procmem: children of pid %d: %w", pid, err)
	}
	var children []int
	for _, task := range tasks {
		raw, err := os.ReadFile(dir + "/" + task.Name() + "/children")
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("procmem: children of pid %d: %w", pid, err)
		}
		for _, field := range strings.Fields(string(raw)) {
			if child, err := strconv.Atoi(field); err == nil {
				children = append(children, child)
			}
		}
	}
	return children, nil
}
