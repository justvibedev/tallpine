package cli

import (
	"slices"
	"strings"
)

type gitWriteKind int

const (
	gitWriteNone gitWriteKind = iota
	gitWriteRemote
	gitWriteSharedConfig
)

type gitWrite struct {
	kind   gitWriteKind
	detail string
}

// gitGlobalsWithValue are git's options before the command that take their value
// as the next argument.
var gitGlobalsWithValue = []string{"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--config-env", "--attr-source"}

// splitGitGlobals separates the options before the git command from the command.
func splitGitGlobals(args []string) (globals, command []string) {
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[:i], args[i:]
		}
		if slices.Contains(gitGlobalsWithValue, args[i]) {
			i++
		}
	}
	return args, nil
}

// classifyGitWrite says what command writes to state linked worktrees share.
// Upstream tracking (push -u, --set-upstream-to) is not one of them: it writes
// only the session's own branch.<name>.* keys, as does gh pr checkout.
func classifyGitWrite(command []string) gitWrite {
	if len(command) == 0 {
		return gitWrite{}
	}
	switch command[0] {
	case "remote":
		return classifyGitRemote(command[1:])
	case "config":
		return classifyGitConfig(command[1:])
	}
	return gitWrite{}
}

// gitRemoteMutations excludes update and prune, which only fetch and prune refs.
var gitRemoteMutations = []string{"add", "rename", "remove", "rm", "set-head", "set-branches", "set-url"}

func classifyGitRemote(args []string) gitWrite {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if slices.Contains(gitRemoteMutations, arg) {
			return gitWrite{kind: gitWriteRemote, detail: arg}
		}
		return gitWrite{}
	}
	return gitWrite{}
}

var (
	gitConfigValueFlags = []string{"--type", "--default", "--comment", "--value", "--url"}
	gitConfigReadFlags  = []string{"--get", "--get-all", "--get-regexp", "--get-urlmatch", "--get-color", "--get-colorbool", "--list", "-l"}
	gitConfigWriteFlags = []string{"--add", "--replace-all", "--unset", "--unset-all", "--rename-section", "--remove-section", "--edit", "-e"}
	// Scopes other than the shared repository config. Git itself refuses
	// --worktree in a linked worktree without extensions.worktreeConfig.
	gitConfigOtherScopes = []string{"--global", "--system", "--worktree", "--file", "-f", "--blob"}
	gitConfigReadVerbs   = []string{"get", "list"}
	gitConfigWriteVerbs  = []string{"set", "unset", "rename-section", "remove-section", "edit"}
)

func classifyGitConfig(args []string) gitWrite {
	var positional []string
	read, write, otherScope := false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, _, hasValue := strings.Cut(arg, "=")
		switch {
		case !strings.HasPrefix(arg, "-"):
			positional = append(positional, arg)
		case slices.Contains(gitConfigReadFlags, name):
			read = true
		case slices.Contains(gitConfigWriteFlags, name):
			write = true
		case slices.Contains(gitConfigOtherScopes, name):
			otherScope = true
			if !hasValue && (name == "--file" || name == "-f" || name == "--blob") {
				i++
			}
		case strings.HasPrefix(arg, "-f"):
			otherScope = true
		case slices.Contains(gitConfigValueFlags, name) && !hasValue:
			i++
		}
	}
	key := ""
	if len(positional) > 0 {
		key = positional[0]
		switch {
		case slices.Contains(gitConfigReadVerbs, key):
			// `git config get a.b` has two positionals and writes nothing.
		case slices.Contains(gitConfigWriteVerbs, key):
			write = true
			key = ""
			if len(positional) > 1 {
				key = positional[1]
			}
		case !read && len(positional) > 1:
			write = true
		}
	}
	switch {
	case !write, otherScope, strings.HasPrefix(key, "branch."):
		return gitWrite{}
	}
	return gitWrite{kind: gitWriteSharedConfig}
}
