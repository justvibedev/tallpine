package kiro

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// ParseTerminalApproval reads the live Kiro TUI approval bar:
//
//	<action> requires approval
//	shell → <command>
//	❯ Allow once
//	  Deny once
//	  Create allow rule to approve future similar actions
//	  Create deny rule to block future similar actions
//	──────
//	esc to close · ↑↓ to navigate · ↵ to select
//
// It reports false unless the bar is the last thing on screen (an answered
// bar leaves the prompt below it) and offers an allow and a deny option.
func (p *Plugin) ParseTerminalApproval(output string) (ports.TerminalApproval, bool) {
	lines := kiroTerminalLines(output)
	header := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(lines[i]), "requires approval") {
			header = i
			break
		}
	}
	if header < 0 || header+2 >= len(lines) {
		return ports.TerminalApproval{}, false
	}
	approval := ports.TerminalApproval{Summary: lines[header], Command: lines[header+1]}
	footer := false
	for _, line := range lines[header+2:] {
		if strings.HasPrefix(line, "─") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "to navigate") {
			footer = true
			break
		}
		selected := strings.HasPrefix(line, "❯")
		label := kiroOptionText(line)
		if label == "" {
			continue
		}
		kind, ok := kiroOptionKind(strings.ToLower(label))
		if !ok {
			return ports.TerminalApproval{}, false
		}
		approval.Options = append(approval.Options, ports.TerminalApprovalOption{
			ID: string(kind), Label: label, Kind: kind, Index: len(approval.Options), Selected: selected,
		})
	}
	if !footer {
		return ports.TerminalApproval{}, false
	}
	var allow, deny bool
	for _, o := range approval.Options {
		allow = allow || o.Kind == ports.ChatDecisionAllowOnce
		deny = deny || o.Kind == ports.ChatDecisionRejectOnce
	}
	return approval, allow && deny
}

func kiroOptionKind(label string) (ports.ChatDecisionKind, bool) {
	switch {
	case strings.HasPrefix(label, "allow once"):
		return ports.ChatDecisionAllowOnce, true
	case strings.HasPrefix(label, "deny once"):
		return ports.ChatDecisionRejectOnce, true
	case strings.Contains(label, "allow rule"):
		return ports.ChatDecisionAllowAlways, true
	case strings.Contains(label, "deny rule"):
		return ports.ChatDecisionRejectAlways, true
	}
	return "", false
}
