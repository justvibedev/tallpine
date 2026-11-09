package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/activitydispatch"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/pricing"
)

// sessionIDPattern bounds the TALLPINE_SESSION_ID we will place in a request path to
// the id alphabet the daemon issues. Validating the externally-set env value
// before it reaches the loopback URL keeps it from steering the request.
// Project ids may contain single dots (e.g. "acme.dev"), and session ids
// inherit them ("acme.dev-2"); dot runs and leading dots stay rejected so the
// id can never be "." or ".." in a request or file path.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)*$`)

const (
	// hooksLogName is the file under TALLPINE_DATA_DIR where hook delivery failures
	// are appended. Agent hook runners swallow stderr, so without a durable
	// sink a dead activity feed (e.g. an unreachable daemon) stays invisible.
	hooksLogName = "hooks.log"
	// maxHooksLogBytes caps hooks.log: an append against a file already past
	// the cap truncates it first, so a persistently failing hook cannot grow
	// the file without bound.
	maxHooksLogBytes = 1 << 20
)

// maxActivityMetaLen bounds the Claude entrypoint forwarded with a hook.
const maxActivityMetaLen = 256

type sessionStartHookOutput struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// newHooksCommand builds the hidden `tallpine hooks <agent> <event>` command that
// agent CLIs invoke from their workspace-local hook config. It reads the native
// hook payload from stdin and the Tallpine session id from TALLPINE_SESSION_ID, derives an
// activity state for the event, and reports it to the daemon.
//
// It is best-effort by design: a hook must never break the user's agent, so a
// non-Tallpine session (no TALLPINE_SESSION_ID), an event that carries no activity signal,
// or an unreachable daemon all exit 0 rather than erroring.
func newHooksCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:    "hooks <agent> <event>",
		Short:  "Receive an agent hook callback (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			hookCtx, cancel := context.WithTimeout(cmd.Context(), hookReportBudget)
			defer cancel()
			return ctx.runHook(hookCtx, args[0], args[1])
		},
	}
}

// hookReportBudget bounds a hook's whole daemon report, retries included.
// Agents run hooks synchronously (Claude's PreToolUse holds the tool call), so
// a stalled daemon must cost the agent seconds rather than the harness's hook
// timeout. Past the budget the hook gives up and exits 0: the signal is lost,
// the agent keeps working.
var hookReportBudget = 5 * time.Second

// activityRetryDelay is the pause before retrying a busy activity projection.
var activityRetryDelay = time.Second

func (c *commandContext) runHook(ctx context.Context, agent, event string) error {
	observedAt := c.deps.Now()
	reviewSessionID := strings.TrimSpace(brand.Getenv("REVIEW_SESSION_ID"))
	if reviewSessionID != "" {
		if !sessionIDPattern.MatchString(reviewSessionID) {
			return nil
		}
		return c.runReviewHook(ctx, agent, event, reviewSessionID)
	}
	sessionID := strings.TrimSpace(brand.Getenv("SESSION_ID"))
	if !sessionIDPattern.MatchString(sessionID) {
		// Not a Tallpine-managed session (unset/empty), or an id we won't put in a
		// request path. Return before reading stdin so a manual invocation
		// without a piped payload can't block on EOF.
		return nil
	}
	payload, err := io.ReadAll(c.deps.In)
	if err != nil {
		// Surface read errors for parity with the daemon-error path, but keep
		// the empty payload and exit 0: a failed hook must not break the
		// agent. The deriver tolerates an empty payload.
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("read stdin: %w", err))
	}

	if err := c.enforceGitGuard(ctx, agent, event, payload); err != nil {
		return err
	}
	facts := activitydispatch.Facts(agent, event, payload)
	if !facts.Reportable {
		// Unknown agent, or an event carrying neither activity nor resumable
		// session metadata: report nothing.
		return nil
	}
	event = facts.Event
	stampClaudeRoute(agent, facts.Usage)
	launchID := validLaunchID(brand.Getenv("RUNTIME_LAUNCH_ID"))
	if launchID == "" {
		launchID = validLaunchID(facts.PayloadLaunchID)
	}
	conversation := facts.Conversation
	path := "sessions/" + url.PathEscape(sessionID) + "/activity"
	req := apitypes.SetActivityRequest{
		ObservedAt:                   observedAt,
		Event:                        event,
		ToolName:                     facts.ToolName,
		ToolUseID:                    facts.ToolUseID,
		SubagentID:                   facts.SubagentID,
		RunningSubagentIDs:           facts.RunningSubagentIDs,
		AgentSessionID:               facts.AgentSessionID,
		LatestUserPrompt:             conversation.LatestUserPrompt,
		LatestAssistantUpdate:        conversation.LatestAssistantUpdate,
		FinalAssistantMessage:        conversation.FinalAssistantMessage,
		TurnUserRequest:              conversation.TurnUserRequest,
		Harness:                      agent,
		ConversationCheckpointOrigin: conversation.CheckpointOrigin,
		CoordinationID:               conversation.CoordinationID,
		ProviderTurnID:               conversation.ProviderTurnID,
		TranscriptPath:               conversation.TranscriptPath,
		LaunchID:                     launchID,
		Usage:                        usageHookMetadata(facts.Usage),
	}
	if facts.HasActivity {
		req.State = string(facts.State)
	}
	if domain.AgentHarness(agent) == domain.HarnessClaudeCode {
		// Claude exports its process kind to hook children. It lets the daemon
		// tell Tallpine's interactive runtime from a side `claude -p` in the same
		// worktree, which inherits this session's env and launch id (#6454).
		if entrypoint := strings.TrimSpace(os.Getenv("CLAUDE_CODE_ENTRYPOINT")); len(entrypoint) <= maxActivityMetaLen {
			req.Entrypoint = entrypoint
		}
		// Claude names the process running the hook. The daemon drops hooks
		// from a Claude process other than the session's terminal one.
		if pid, err := strconv.Atoi(strings.TrimSpace(os.Getenv("CLAUDE_PID"))); err == nil && pid > 1 {
			req.ProcessID = pid
		}
	}
	if domain.AgentHarness(agent) == domain.HarnessClaudeCode && event == "user-prompt-submit" &&
		launchID != "" && facts.AgentSessionID != "" && conversation.CheckpointOrigin != domain.ConversationCheckpointOriginUnknown {
		req.SubmissionID = uuid.NewString()
		output := sessionStartHookOutput{}
		output.HookSpecificOutput.HookEventName = "UserPromptSubmit"
		output.HookSpecificOutput.AdditionalContext = domain.NativeSubmissionContext(req.SubmissionID)
		if err := json.NewEncoder(c.deps.Out).Encode(output); err != nil {
			return fmt.Errorf("emit native submission correlation: %w", err)
		}
	}
	if err := c.postActivityHook(ctx, path, req); err != nil {
		// Surface the failure for diagnosis, but exit 0: a failed activity
		// report must not disrupt the agent.
		c.reportHookFailure(agent, event, sessionID, err)
	}
	return nil
}

func (c *commandContext) postActivityHook(ctx context.Context, path string, req apitypes.SetActivityRequest) error {
	for attempt := 0; ; attempt++ {
		err := c.postJSON(ctx, path, req, nil)
		var response apiResponseError
		// Only this response guarantees the signal did not commit. Retrying
		// transport errors or generic 503s could duplicate an applied Stop.
		if attempt >= 3 || !errors.As(err, &response) || response.StatusCode != http.StatusServiceUnavailable ||
			response.ErrorBody.Code != "ACTIVITY_PROJECTION_BUSY" {
			return err
		}
		timer := time.NewTimer(activityRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *commandContext) runReviewHook(ctx context.Context, agent, event, reviewSessionID string) error {
	payload, err := io.ReadAll(c.deps.In)
	if err != nil {
		c.reportHookFailure(agent, event, reviewSessionID, fmt.Errorf("read stdin: %w", err))
	}
	if err := c.enforceGitGuard(ctx, agent, event, payload); err != nil {
		return err
	}
	if domain.AgentHarness(agent) == domain.HarnessClaudeCode && event == "permission-request" {
		// Answered here, so the reviewer never parks in blocked (#4810).
		out := reviewerPermissionDecision(payload, strings.TrimSpace(brand.Getenv("REVIEW_WORKER_SESSION_ID")), brand.Getenv("REVIEW_PRS"))
		if err := json.NewEncoder(c.deps.Out).Encode(out); err != nil {
			c.reportHookFailure(agent, event, reviewSessionID, fmt.Errorf("write permission response: %w", err))
		}
		return nil
	}
	facts := activitydispatch.Facts(agent, event, payload)
	if !facts.HasActivity && facts.NativeSessionID == "" {
		return nil
	}
	launchID := validLaunchID(brand.Getenv("RUNTIME_LAUNCH_ID"))
	if launchID == "" {
		launchID = validLaunchID(facts.PayloadLaunchID)
	}
	path := "reviews/" + url.PathEscape(reviewSessionID) + "/activity"
	req := apitypes.SetReviewActivityRequest{
		Event:          event,
		AgentSessionID: facts.NativeSessionID,
		LaunchID:       launchID,
	}
	if facts.HasActivity {
		req.State = string(facts.State)
	}
	if err := c.postJSON(ctx, path, req, nil); err != nil {
		c.reportHookFailure(agent, event, reviewSessionID, err)
	}
	return nil
}

func validLaunchID(value string) string {
	value = strings.TrimSpace(value)
	if !sessionIDPattern.MatchString(value) {
		return ""
	}
	return value
}

// reportHookFailure surfaces a hook delivery failure without breaking the
// agent: stderr for the agent's hook runner, plus a best-effort append to
// $TALLPINE_DATA_DIR/hooks.log so the failure can be diagnosed after the fact.
func (c *commandContext) reportHookFailure(agent, event, sessionID string, cause error) {
	msg := fmt.Sprintf("tallpine hooks %s %s: %v", agent, event, cause)
	if !errors.Is(cause, errDaemonNotRunning) {
		_, _ = fmt.Fprintln(c.deps.Err, msg)
	}
	dataDir, ok := config.DataDirFromEnv()
	if !ok {
		return
	}
	line := fmt.Sprintf("%s session=%s %s\n", time.Now().UTC().Format(time.RFC3339), sessionID, msg)
	appendHooksLog(dataDir, line)
}

// appendHooksLog appends one line to the hooks log, truncating first when the
// file has outgrown maxHooksLogBytes. Errors are dropped: this sink is itself
// best-effort and has nowhere better to report.
func appendHooksLog(dataDir, line string) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil { // #nosec G703 -- path is rooted in the daemon data dir plus a fixed file name
		return
	}
	path := filepath.Join(dataDir, hooksLogName)
	flags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if info, err := os.Stat(path); err == nil && info.Size() > maxHooksLogBytes { // #nosec G703 -- path is rooted in the daemon data dir plus a fixed file name
		flags = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}
	f, err := os.OpenFile(path, flags, 0o600) // #nosec G703 -- path is rooted in Tallpine's own data dir
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.WriteString(line)
}

// stampClaudeRoute records the billing route this hook process runs under on a
// Claude usage report. The route comes from the process environment, not the
// payload, so it is set here rather than in activitydispatch.Facts.
func stampClaudeRoute(agent string, usage *activitydispatch.UsageMetadata) {
	if usage != nil && domain.AgentHarness(agent) == domain.HarnessClaudeCode {
		usage.ProviderID = pricing.ClaudeRouteFromEnv(os.Getenv)
	}
}

// usageHookMetadata maps the dispatcher's usage facts to the wire type.
func usageHookMetadata(u *activitydispatch.UsageMetadata) *apitypes.UsageHookMetadata {
	if u == nil {
		return nil
	}
	return &apitypes.UsageHookMetadata{
		Harness: domain.AgentHarness(u.Harness), ProviderID: u.ProviderID, TranscriptPath: u.TranscriptPath,
		ModelID: u.ModelID, SubagentID: u.SubagentID, SubagentTranscriptPath: u.SubagentTranscriptPath,
	}
}
