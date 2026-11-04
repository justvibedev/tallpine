package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

func modelCommand(ctx context.Context, binary string, args []string, workingDir string, env map[string]string) *exec.Cmd {
	cmd := tallpineprocess.CommandContext(ctx, binary, args...) //nolint:gosec // binary is adapter-resolved, args are static
	cmd.WaitDelay = commandTerminationWait
	if strings.TrimSpace(workingDir) != "" {
		cmd.Dir = workingDir
	}
	cmd.Env = mergedEnvironment(os.Environ(), env)
	return cmd
}

func mergedEnvironment(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(base)+len(keys))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := overrides[key]; !replaced {
			out = append(out, item)
		}
	}
	for _, key := range keys {
		out = append(out, key+"="+overrides[key])
	}
	return out
}

func modelDiscoveryError(runCtx context.Context, agentID string, commandErr error, output []byte) error {
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s model discovery timed out after %s", agentID, commandTimeout)
	}
	if errors.Is(runCtx.Err(), context.Canceled) {
		return fmt.Errorf("%s model discovery canceled: %w", agentID, context.Canceled)
	}
	// A CLI's own stderr is the only place it explains a non-zero exit (a bad
	// config, a missing provider, a parse failure). Discarding it turns every
	// failure into an opaque "exit status 1"; surfacing a bounded tail makes the
	// cause visible in the cached RefreshError and the logs.
	if detail := discoveryErrorDetail(output); detail != "" {
		return fmt.Errorf("%s model discovery: %w: %s", agentID, commandErr, detail)
	}
	return fmt.Errorf("%s model discovery: %w", agentID, commandErr)
}

// discoveryErrorDetailMax bounds how much command output a discovery error
// carries — enough to show the CLI's explanation without letting a chatty tool
// flood the error string or the logs.
const discoveryErrorDetailMax = 500

// discoveryErrorDetail returns a bounded, single-line, ANSI-stripped tail of a
// failed model command's combined output. The tail is where a CLI's error
// summary lands after any progress noise; whitespace is collapsed so the result
// is a single log-friendly line.
func discoveryErrorDetail(output []byte) string {
	plain := ansiPattern.ReplaceAllString(string(output), "")
	// A Node CLI that crashed ends on its stack; the error is the line above it.
	if line := thrownErrorLine(plain); line != "" {
		plain = line
	}
	cleaned := strings.Join(strings.Fields(plain), " ")
	if cleaned == "" {
		return ""
	}
	if runes := []rune(cleaned); len(runes) > discoveryErrorDetailMax {
		cleaned = "…" + string(runes[len(runes)-discoveryErrorDetailMax:])
	}
	return cleaned
}

// thrownErrorPattern is the line a crashed Node or Python CLI names its error on.
var thrownErrorPattern = regexp.MustCompile(`(?m)^\s*(?:Uncaught )?(?:[A-Z][A-Za-z]*)?(?:Error|Exception): .+$`)

// stackFramePattern is a line of a stack trace.
var stackFramePattern = regexp.MustCompile(`(?m)^\s+at \S`)

// thrownErrorLine is the last such line of output that ends on a stack trace,
// or "" when output is not a crash.
func thrownErrorLine(output string) string {
	if !stackFramePattern.MatchString(output) {
		return ""
	}
	matches := thrownErrorPattern.FindAllString(output, -1)
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimSpace(matches[len(matches)-1])
}
