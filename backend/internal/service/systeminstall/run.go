package systeminstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// run executes argv in the background and records the outcome onto job. job
// is only ever mutated here and read back through a copy under s.mu, so
// concurrent Start/Status calls never race with this goroutine's writes.
// The run is bounded by installTimeout so a stalled installer eventually
// surfaces as a failure instead of pinning the target in StatusRunning.
func (s *Service) run(parent context.Context, argv []string, job *Job) {
	ctx, cancel := context.WithTimeout(parent, s.installTimeout)
	defer cancel()

	out := &capturedOutput{max: maxOutputBytes}
	var runErr error
	if s.installCommands != nil {
		runErr = s.installCommands.RunInstall(ctx, ports.InstallCommand{
			Argv: argv,
			Env: []string{
				"CI=1",
				"NONINTERACTIVE=1",
				"HOMEBREW_NO_AUTO_UPDATE=1",
				"NPM_CONFIG_AUDIT=false",
				"NPM_CONFIG_FUND=false",
			},
		}, out, out)
	} else {
		runErr = s.commands.Run(ctx, argv, out, out)
	}
	now := time.Now()

	s.mu.Lock()
	job.Output = out.String()
	job.FinishedAt = &now
	if ctx.Err() == context.DeadlineExceeded {
		job.Status = StatusFailed
		job.Error = fmt.Sprintf("install timed out after %s", s.installTimeout)
		s.mu.Unlock()
		return
	}
	if ctx.Err() == context.Canceled {
		job.Status = StatusInterrupted
		job.Error = "daemon shutdown interrupted the install"
		s.mu.Unlock()
		return
	}
	if runErr != nil {
		job.Status = StatusFailed
		job.Error = runErr.Error()
		s.mu.Unlock()
		return
	}
	if !IsAgentTarget(job.Target) {
		if path, err := s.executables.LookPath(string(job.Target)); err != nil || path == "" {
			job.Status = StatusFailed
			job.Error = fmt.Sprintf("install command finished but %s is still not in PATH", job.Target)
			s.mu.Unlock()
			return
		}
	}
	job.Status = StatusSucceeded
	callback := s.onSucceeded
	target := job.Target
	s.mu.Unlock()
	if callback != nil {
		callback(target)
	}
}

func (s *Service) runAgentOperation(parent context.Context, plan Plan, operation AgentOperation, job *Job, baseline *installedBaseline) {
	ctx, cancel := context.WithTimeout(parent, s.installTimeout)
	defer cancel()
	out := &capturedOutput{max: maxOutputBytes}
	outputWriter := io.Writer(out)
	env := []string{
		"CI=1", "NONINTERACTIVE=1", "HOMEBREW_NO_AUTO_UPDATE=1",
		"NPM_CONFIG_AUDIT=false", "NPM_CONFIG_FUND=false",
	}
	if plan.keepPreviousRelease && plan.Method == "homebrew" && !plan.PackageCask {
		// Live sessions still run the old keg; it is cleaned up after they move.
		env = append(env, "HOMEBREW_NO_INSTALL_CLEANUP=1")
		s.deferHomebrewCleanup(plan.Target, packageWithoutLatest(plan.Package))
	}
	var runErr error
	if plan.managed {
		runErr = s.runManagedInstall(ctx, plan, baseline, env, outputWriter)
	} else if plan.Script != nil {
		if s.installScripts == nil {
			runErr = errors.New("remote installer runner is not configured")
		} else {
			command := *plan.Script
			command.Env = append(append([]string(nil), env...), command.Env...)
			var result ports.InstallScriptResult
			result, runErr = s.installScripts.RunInstallScript(ctx, command, outputWriter, outputWriter)
			if result.SHA256 != "" {
				_, _ = fmt.Fprintf(out, "\nsource: %s\nsha256: %s\n", command.URL, result.SHA256)
			}
		}
	} else {
		if plan.Method == "official-installer" {
			env = append(env, vendorUpdateEnv[plan.Target]...)
		}
		runCommand := func(argv []string) error {
			if s.installCommands != nil {
				return s.installCommands.RunInstall(ctx, ports.InstallCommand{Argv: argv, Env: env}, out, out)
			}
			return s.commands.Run(ctx, argv, out, out)
		}
		for _, argv := range plan.Prepare {
			if err := runCommand(argv); err != nil && ctx.Err() == nil {
				_, _ = fmt.Fprintf(out, "\n%s failed (%v); continuing.\n", strings.Join(argv, " "), err)
			}
		}
		if ctx.Err() == nil {
			runErr = runCommand(plan.Command)
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		s.finishAgentJob(job, StatusFailed, out.String(), fmt.Sprintf("%s timed out after %s", operation, s.installTimeout), "")
		return
	}
	if ctx.Err() == context.Canceled {
		s.finishAgentJob(job, StatusInterrupted, out.String(), fmt.Sprintf("daemon shutdown interrupted %s", operation), "")
		return
	}
	if runErr != nil {
		s.finishAgentJob(job, StatusFailed, out.String(), failureSummary(runErr, out), "")
		return
	}

	if err := s.transitionAgentJob(job, StatusVerifying, out.String(), "", ""); err != nil {
		s.finishAgentJob(job, StatusFailed, "", fmt.Sprintf("persist verifying state: %v", err), "")
		return
	}
	updaterReport := ""
	if plan.Script == nil {
		updaterReport = lastOutputLine(out.String())
	}
	s.runAgentVerification(s.backgroundContext, job, baseline, updaterReport)
}

// maxUpdaterReportRunes keeps the updater line quoted in a failed update
// short enough for the update notice.
const maxUpdaterReportRunes = 80

// installFailure is a failed operation with a short summary for the update
// notice. err, which may name paths or quote output, goes to diagnostics.
type installFailure struct {
	summary string
	err     error
}

func (e *installFailure) Error() string { return e.summary }
func (e *installFailure) Unwrap() error { return e.err }

func installFailed(summary string, err error) error {
	return &installFailure{summary: summary, err: err}
}

// failureSummary is the job error for err. An installFailure's detail is
// written to out, the job's diagnostics, so the notice keeps only the summary.
func failureSummary(err error, out io.Writer) string {
	var failure *installFailure
	if errors.As(err, &failure) && failure.err != nil {
		_, _ = fmt.Fprintf(out, "\n%s\n%v\n", failure.summary, failure.err)
	}
	return err.Error()
}

// terminalEscape matches ANSI CSI and OSC sequences that colour or retitle
// terminal output.
var terminalEscape = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// lastOutputLine is the last non-empty line a command printed, as a terminal
// would show it: escape sequences removed and only the text after a line's
// last carriage return, shortened to fit an error message.
func lastOutputLine(output string) string {
	output = terminalEscape.ReplaceAllString(strings.ReplaceAll(output, "\r\n", "\n"), "")
	lines := strings.Split(output, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		// A terminal keeps the last carriage-return segment that printed text,
		// including one followed by a trailing bare \r.
		line := ""
		segments := strings.Split(lines[index], "\r")
		for segment := len(segments) - 1; segment >= 0 && line == ""; segment-- {
			line = strings.TrimSpace(segments[segment])
		}
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > maxUpdaterReportRunes {
			line = string(runes[:maxUpdaterReportRunes]) + "…"
		}
		return line
	}
	return ""
}

// capturedOutput is an io.Writer that keeps only the last max bytes written,
// trimming from the front.
type capturedOutput struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (c *capturedOutput) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Write(p)
	if c.buf.Len() > c.max {
		tail := c.buf.String()[c.buf.Len()-c.max:]
		c.buf.Reset()
		c.buf.WriteString(tail)
	}
	return len(p), nil
}

func (c *capturedOutput) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}
