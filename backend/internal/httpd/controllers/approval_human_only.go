package controllers

import (
	"encoding/json"
	"regexp"
	"strings"
)

// humanOnlyApprovals are the request classes an orchestrator may never allow on
// a worker's behalf, even with orchestratorApprovals on. Each would let a
// prompt-injected worker reach past its session: Tallpine's own secrets and
// store, Keychain dumps, private keys, the agent harnesses' config, and the
// shell or launchd startup files that run outside any session. A match leaves
// the request to the human; the orchestrator may still reject it.
//
// Matching is on the whole request (summary and provider detail), case
// insensitive, so a path in a Read/Edit input and the same path inside a shell
// command are caught alike. A false positive costs one human click; a miss
// costs the boundary, so the patterns lean broad.
var humanOnlyApprovals = []struct {
	class   string
	pattern *regexp.Regexp
}{
	{"Tallpine secrets and database", regexp.MustCompile(`daemon\.token|session\.key|\bao\.db|github-pat\.json|browser-runtime\.key|/\.tallpine/electron\b|tallpine_daemon_auth`)},
	{"Keychain access", regexp.MustCompile(`\bsecurity\s+(find-(generic|internet|certificate)-password|find-certificate|dump-keychain|export|unlock-keychain)\b`)},
	{"SSH keys and credentials", regexp.MustCompile(`/\.ssh/|\.aws/credentials|\.config/gh/hosts\.yml|\.netrc\b|\.git-credentials`)},
	{"agent harness config", regexp.MustCompile(`/\.claude/|/\.claude\.json|/\.codex/`)},
	{"shell and login startup files", regexp.MustCompile(`/\.(zshrc|zshenv|zprofile|zlogin|bashrc|bash_profile|profile)\b|launchagents|launchctl\s+(setenv|load|bootstrap)`)},
}

// humanOnlyClass returns the class of a request an orchestrator may not allow,
// or "" when it may.
func humanOnlyClass(summary string, detail json.RawMessage) string {
	text := strings.ToLower(summary + "\n" + string(detail) + "\n" + approvalCommand(detail))
	for _, c := range humanOnlyApprovals {
		if c.pattern.MatchString(text) {
			return c.class
		}
	}
	return ""
}
