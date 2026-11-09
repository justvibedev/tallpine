package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
)

const (
	// hookEventGitGuard runs only the guard, for launches that report activity
	// some other way (Codex chat) and must not also report it through a hook.
	hookEventGitGuard = "git-guard"
	hookEventPreTool  = "pre-tool-use"
	// hookBlockExit is the status Codex and Kiro both treat as "do not run this
	// tool", passing stderr to the model. Any other failure lets the tool run.
	hookBlockExit = 2
	// gitHookUnresolved stands in for a shell word whose value is only known when
	// the shell runs. It has no leading dash, so it classifies as a plain operand.
	gitHookUnresolved = "__tallpine_unresolved__"
	// gitHookMaxDepth bounds nested `bash -c` and eval parsing.
	gitHookMaxDepth = 4
)

var (
	gitHookShells = []string{"sh", "bash", "zsh", "dash", "ksh"}
	// gitHookWrappers run the command named by their remaining arguments.
	gitHookWrappers = []string{"command", "env", "exec", "nice", "nohup", "sudo", "time", "timeout"}
)

// enforceGitGuard refuses a tool call that would write the remotes or config a
// linked worktree shares with the owner's checkout. It backs the PATH shim for
// harnesses whose shells re-run the user's rc files, which can put the real git
// back in front of it (B600). Claude Code keeps the launch PATH, so the shim is
// enough there. The hook reads what the agent asked for, so a write hidden inside
// a script still depends on the shim; docs/agent-policy.md states the limit.
func (c *commandContext) enforceGitGuard(ctx context.Context, agent, event string, payload []byte) error {
	applies := event == hookEventGitGuard ||
		(event == hookEventPreTool && domain.AgentHarness(agent) != domain.HarnessClaudeCode)
	if !applies {
		return nil
	}
	reason := gitHookRefusal(ctx, payload)
	if reason == "" {
		return nil
	}
	_, _ = fmt.Fprintf(c.deps.Err, "tallpine git: %s\n", reason)
	return exitCodeError{code: hookBlockExit, silent: true}
}

// gitHookRefusal is why the tool call in payload must not run, empty when it
// may. Any tool with a string `command` input is scanned, so a harness renaming
// its shell tool does not silently switch the guard off; a patch is the one
// exception, because its body is file text that can quote git commands.
func gitHookRefusal(ctx context.Context, payload []byte) string {
	if strings.TrimSpace(os.Getenv(agentlaunch.EnvGitGuardRoot)) == "" {
		return ""
	}
	var call struct {
		CWD       string `json:"cwd"`
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command json.RawMessage `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(payload, &call) != nil {
		return ""
	}
	var command string
	if json.Unmarshal(call.ToolInput.Command, &command) != nil || !strings.Contains(command, "git") {
		return ""
	}
	if call.ToolName == "apply_patch" || strings.HasPrefix(strings.TrimSpace(command), "*** Begin Patch") {
		return ""
	}
	gitPath, err := findOnPathOutside("git", os.Getenv("PATH"), "")
	if err != nil {
		return ""
	}
	scan := gitHookScan{ctx: ctx, gitPath: gitPath}
	scan.scan(command, &gitHookScope{dir: call.CWD}, 0)
	return scan.reason
}

// gitHookScope is the shell state that decides what a later git command means.
type gitHookScope struct {
	// dir is the working directory, empty once a `cd` cannot be followed.
	dir string
	// allow records an exported override.
	allow bool
}

type gitHookScan struct {
	ctx     context.Context
	gitPath string
	reason  string
}

func (s *gitHookScan) scan(command string, scope *gitHookScope, depth int) {
	if depth > gitHookMaxDepth || s.reason != "" {
		return
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		s.scanSegments(command, scope, depth)
		return
	}
	s.walk(file, scope, depth)
}

// scanSegments covers text the parser rejects, such as zsh-only syntax: a git
// command in it should not get through because of the syntax around it. Only the
// start of a segment counts, so git named in an echo or a quoted string does not.
func (s *gitHookScan) scanSegments(command string, scope *gitHookScope, depth int) {
	for _, segment := range gitHookSegments(command) {
		tokens := strings.Fields(segment)
		env := map[string]string{}
		for len(tokens) > 0 {
			key, value, ok := strings.Cut(tokens[0], "=")
			if !ok || !syntax.ValidName(key) {
				break
			}
			env[key] = strings.Trim(value, `'"`)
			tokens = tokens[1:]
		}
		for i := range tokens {
			tokens[i] = strings.Trim(tokens[i], `'"`)
		}
		s.call(tokens, env, scope, depth)
	}
}

// gitHookSegments splits command at shell separators outside quotes.
func gitHookSegments(command string) []string {
	var segments []string
	var b strings.Builder
	var quote rune
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case strings.ContainsRune(";&|()`\n", r):
			segments = append(segments, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	return append(segments, b.String())
}

func (s *gitHookScan) walk(root syntax.Node, scope *gitHookScope, depth int) {
	isolated := func(stmts []*syntax.Stmt) {
		inner := *scope
		for _, stmt := range stmts {
			s.walk(stmt, &inner, depth)
		}
	}
	syntax.Walk(root, func(node syntax.Node) bool {
		if s.reason != "" {
			return false
		}
		switch n := node.(type) {
		case *syntax.Subshell:
			isolated(n.Stmts)
			return false
		case *syntax.CmdSubst:
			isolated(n.Stmts)
			return false
		case *syntax.ProcSubst:
			isolated(n.Stmts)
			return false
		case *syntax.DeclClause:
			if n.Variant != nil && n.Variant.Value == "export" && gitHookAssigns(n.Args)[agentlaunch.EnvAllowSharedGitConfig] == "1" {
				scope.allow = true
			}
		case *syntax.Stmt:
			s.heredoc(n, scope, depth)
		case *syntax.CallExpr:
			s.call(gitHookWords(n.Args), gitHookAssigns(n.Assigns), scope, depth)
		}
		return true
	})
}

// heredoc scans a script fed to a shell on stdin (`bash <<EOF`).
func (s *gitHookScan) heredoc(stmt *syntax.Stmt, scope *gitHookScope, depth int) {
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return
	}
	args, _ := unwrapGitHookWrappers(gitHookWords(call.Args), nil)
	if len(args) == 0 || !slices.Contains(gitHookShells, path.Base(args[0])) {
		return
	}
	for _, redirect := range stmt.Redirs {
		if (redirect.Op == syntax.Hdoc || redirect.Op == syntax.DashHdoc) && redirect.Hdoc != nil {
			inner := *scope
			s.scan(gitHookWord(redirect.Hdoc), &inner, depth+1)
		}
	}
}

func (s *gitHookScan) call(args []string, env map[string]string, scope *gitHookScope, depth int) {
	args, env = unwrapGitHookWrappers(args, env)
	if len(args) == 0 {
		return
	}
	switch name := path.Base(args[0]); {
	case name == "cd" || name == "pushd":
		scope.dir = gitHookChdir(scope.dir, args[1:])
	case name == "popd":
		scope.dir = ""
	case name == "git":
		s.git(args[1:], env, scope)
	case name == "eval":
		s.scan(strings.Join(args[1:], " "), scope, depth+1)
	case name == "xargs":
		if i := slices.IndexFunc(args, func(arg string) bool { return path.Base(arg) == "git" }); i > 0 {
			s.git(args[i+1:], env, scope)
		}
	case slices.Contains(gitHookShells, name):
		// The inner shell's `cd` cannot move this one.
		inner := *scope
		if script, ok := gitHookShellScript(args[1:]); ok {
			s.scan(script, &inner, depth+1)
		}
	}
}

// git classifies one git invocation. A location it cannot read is left alone:
// `cd "$(mktemp -d)" && git init && git remote add` is ordinary scratch work.
func (s *gitHookScan) git(args []string, env map[string]string, scope *gitHookScope) {
	if s.reason != "" || scope.allow || env[agentlaunch.EnvAllowSharedGitConfig] == "1" || scope.dir == "" {
		return
	}
	if gitDir, ok := env["GIT_DIR"]; ok {
		args = append([]string{"--git-dir=" + gitDir}, args...)
	}
	// An unresolved -C or --git-dir names no real path, so git reports no
	// repository and gitRefusalIn leaves the command alone.
	s.reason = gitRefusalIn(s.ctx, s.gitPath, scope.dir, args)
}

func gitHookChdir(dir string, args []string) string {
	var target string
	for _, arg := range args {
		if arg != "-" && strings.HasPrefix(arg, "-") {
			continue
		}
		target = arg
		break
	}
	switch {
	case target == "", target == "-", strings.Contains(target, gitHookUnresolved), strings.HasPrefix(target, "~"):
		return ""
	case filepath.IsAbs(target):
		return filepath.Clean(target)
	case dir == "":
		return ""
	}
	return filepath.Join(dir, target)
}

// gitHookShellScript returns the script of `sh -c script`: the first operand
// after the options, once a short-option cluster has included c. Long options
// and the values of -o, +o, -O, +O, --rcfile and --init-file come before it.
func gitHookShellScript(args []string) (string, bool) {
	sawC := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--rcfile" || arg == "--init-file":
			i++
		case strings.HasPrefix(arg, "--") && arg != "--":
		case len(arg) > 1 && (arg[0] == '-' || arg[0] == '+'):
			sawC = sawC || (arg[0] == '-' && strings.Contains(arg, "c"))
			if strings.ContainsAny(arg, "oO") {
				i++
			}
		case arg == "--":
		default:
			return arg, sawC
		}
	}
	return "", false
}

// unwrapGitHookWrappers strips commands that only run another command, keeping
// the environment they set. `command -v git` looks git up and runs nothing.
func unwrapGitHookWrappers(args []string, env map[string]string) ([]string, map[string]string) {
	for len(args) > 0 && slices.Contains(gitHookWrappers, path.Base(args[0])) {
		name := path.Base(args[0])
		args = args[1:]
		if name == "command" && len(args) > 0 && (args[0] == "-v" || args[0] == "-V") {
			return nil, env
		}
		for len(args) > 0 && strings.HasPrefix(args[0], "-") {
			takesValue := (name == "nice" && args[0] == "-n") || (name == "env" && args[0] == "-u") ||
				(name == "timeout" && (args[0] == "-k" || args[0] == "-s")) || (name == "sudo" && args[0] == "-u")
			args = args[1:]
			if takesValue && len(args) > 0 {
				args = args[1:]
			}
		}
		for name == "env" && len(args) > 0 && strings.Contains(args[0], "=") {
			key, value, _ := strings.Cut(args[0], "=")
			if env == nil {
				env = map[string]string{}
			}
			env[key] = value
			args = args[1:]
		}
		if name == "timeout" && len(args) > 0 {
			args = args[1:]
		}
	}
	return args, env
}

func gitHookWords(words []*syntax.Word) []string {
	out := make([]string, 0, len(words))
	for _, word := range words {
		out = append(out, gitHookWord(word))
	}
	return out
}

// gitHookAssigns reads NAME=value prefixes and export arguments.
func gitHookAssigns(assigns []*syntax.Assign) map[string]string {
	out := map[string]string{}
	for _, assign := range assigns {
		if assign.Name == nil {
			continue
		}
		value := ""
		if assign.Value != nil {
			value = gitHookWord(assign.Value)
		}
		out[assign.Name.Value] = value
	}
	return out
}

// gitHookWord is word as the shell would pass it, with gitHookUnresolved in place
// of any expansion.
func gitHookWord(word *syntax.Word) string {
	var b strings.Builder
	gitHookParts(&b, word.Parts)
	return b.String()
}

func gitHookParts(b *strings.Builder, parts []syntax.WordPart) {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(gitHookUnescape(p.Value))
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			gitHookParts(b, p.Parts)
		default:
			b.WriteString(gitHookUnresolved)
		}
	}
}

func gitHookUnescape(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var b strings.Builder
	escaped := false
	for _, r := range value {
		if r == '\\' && !escaped {
			escaped = true
			continue
		}
		b.WriteRune(r)
		escaped = false
	}
	return b.String()
}
