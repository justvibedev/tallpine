package codexappserver

import (
	"encoding/json"
	"slices"
	"strings"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/brand"
)

// Codex's workspace-write sandbox cannot reach the daemon socket, so every
// `tallpine` call from an auto or accept-edits session raises an approval. The
// call is how the session reports to its orchestrator, so approving exactly that
// command spares the user a prompt per call. Nothing else is auto-approved and
// the sandbox is unchanged.

// tallpineSubcommands are the CLI calls a worker needs from inside the sandbox.
// Everything else is left to the user: some subcommands start arbitrary programs
// (agent-process supervise) or write host files (browser), and an approved command
// runs with no sandbox at all.
var tallpineSubcommands = [][]string{
	{"report"}, {"send"}, {"commit"}, {"session", "get"}, {"session", "usage"}, {"session", "ls"},
}

// approvedShells are the only programs accepted as a `-c` wrapper, matched as the
// whole prefix: no arguments, and never an arbitrary path (a worker can write a
// file called zsh into its workspace).
var approvedShells = map[string]bool{
	"/bin/zsh": true, "/bin/bash": true, "/bin/sh": true, "zsh": true, "bash": true, "sh": true,
}

// shellInner returns the script of `<shell> -lc '<script>'` (or -c), where the
// shell is exactly one of approvedShells and the script is one quoted argument
// with no further quote of its own kind inside. Any other shape returns false.
func shellInner(command string) (string, bool) {
	for _, flag := range []string{" -lc ", " -c "} {
		prefix, rest, found := strings.Cut(command, flag)
		if !found || !approvedShells[prefix] {
			continue
		}
		rest = strings.TrimSpace(rest)
		if len(rest) < 2 || (rest[0] != '\'' && rest[0] != '"') || rest[len(rest)-1] != rest[0] {
			return "", false
		}
		inner := rest[1 : len(rest)-1]
		if strings.ContainsRune(inner, rune(rest[0])) {
			return "", false
		}
		return inner, true
	}
	return "", false
}

// isTallpineCLICommand reports whether command is an allowlisted invocation of
// the Tallpine CLI: either the bare line, or exactly one approved shell wrapping
// it. argv[0] is the bare name (never a path), the subcommand is on the allowlist,
// and the whole line uses only characters that cannot chain, redirect, substitute
// or expand into another command.
//
// Assumption: the bare name resolves through a PATH the sandboxed worker cannot
// write; Tallpine launches sessions with its own CLI directory first.
func isTallpineCLICommand(command string) bool {
	line := strings.TrimSpace(command)
	if inner, ok := shellInner(line); ok {
		line = strings.TrimSpace(inner)
	}
	if line == "" {
		return false
	}
	for _, r := range line {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune(" _./:=,@+-", r):
		default:
			return false
		}
	}
	fields := strings.Fields(line)
	if fields[0] != brand.CLIName && fields[0] != brand.CLIAlias {
		return false
	}
	args := fields[1:]
	for _, sub := range tallpineSubcommands {
		if len(args) >= len(sub) && slices.Equal(args[:len(sub)], sub) {
			return true
		}
	}
	return false
}

// autoApproveTallpineCLI reports whether a command-execution approval is a plain
// Tallpine CLI call that offers a one-time accept. Network and file-change
// approvals are never matched.
func autoApproveTallpineCLI(method string, params json.RawMessage) bool {
	if method != codexproto.MethodItemCommandExecutionRequestApproval {
		return false
	}
	var p codexproto.CommandExecutionRequestApprovalParams
	if json.Unmarshal(params, &p) != nil || p.Command == nil || p.NetworkApprovalContext != nil {
		return false
	}
	if !isTallpineCLICommand(*p.Command) {
		return false
	}
	var raw struct {
		Available []json.RawMessage `json:"availableDecisions"`
	}
	_ = json.Unmarshal(params, &raw)
	for _, d := range raw.Available {
		var s string
		if json.Unmarshal(d, &s) == nil && s == "accept" {
			return true
		}
	}
	return false
}
