package localland

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// git runs git in dir (ports.DefaultGit().Plain: writes and push keep the
// user's credentials) with hooks off and no prompts. stdin may be empty.
func git(ctx context.Context, dir, stdin string, args ...string) (string, error) {
	return gitEnv(ctx, dir, stdin, nil, args...)
}

// gitEnv is git with extra environment entries for this process only.
func gitEnv(ctx context.Context, dir, stdin string, extraEnv []string, args ...string) (string, error) {
	full := append(ports.GitHardenedArgs(), args...)
	cmd := ports.DefaultGit().Plain(ctx, dir, full...)
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, extraEnv...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		return out.String(), &gitError{args: args, stderr: strings.TrimSpace(errb.String()), err: err}
	}
	return strings.TrimSpace(out.String()), nil
}

type gitError struct {
	args   []string
	stderr string
	err    error
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.stderr)
}

func (e *gitError) Unwrap() error { return e.err }

// exitCode is git's exit status, or -1 when git did not run to an exit.
func exitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// revParse resolves ref to a commit, reporting ok=false when it does not exist.
func revParse(ctx context.Context, dir, ref string) (string, bool, error) {
	out, err := git(ctx, dir, "", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", false, nil
		}
		return "", false, err
	}
	return out, true, nil
}

func isAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	_, err := git(ctx, dir, "", "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	if exitCode(err) == 1 {
		return false, nil
	}
	return false, err
}

// mergeTree merges head into base without touching any checkout. It returns
// the result tree, or the conflicted paths.
func mergeTree(ctx context.Context, dir, base, head string) (string, []string, error) {
	out, err := git(ctx, dir, "", "merge-tree", "--write-tree", "--name-only", "--no-messages", base, head)
	if err != nil && exitCode(err) != 1 {
		return "", nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err == nil {
		return strings.TrimSpace(lines[0]), nil, nil
	}
	var files []string
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return "", files, nil
}

// findLanded returns the commit on tip's history that carries the
// Tallpine-Head trailer for head.
func findLanded(ctx context.Context, dir, tip, head string) (string, error) {
	return git(ctx, dir, "", "log", "-1", "--format=%H", "--fixed-strings", "--grep="+headTrailer+" "+head, tip)
}

// squashMessage: subject, the branch's commit subjects, then the trailers.
func squashMessage(ctx context.Context, dir, subject, base, head, session string) (string, error) {
	mb, err := git(ctx, dir, "", "merge-base", base, head)
	if err != nil {
		return "", err
	}
	subjects, err := git(ctx, dir, "", "log", "--reverse", "--format=* %s", mb+".."+head)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(subject + "\n\n")
	if subjects != "" {
		b.WriteString(subjects + "\n\n")
	}
	fmt.Fprintf(&b, "%s %s\n%s %s\n", sessionTrailer, session, headTrailer, head)
	return b.String(), nil
}

const (
	sessionTrailer = "Tallpine-Session:"
	headTrailer    = "Tallpine-Head:"
)
