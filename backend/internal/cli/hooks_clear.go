package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

// claudeHookContextMaxChars is how much hook text Claude Code adds to the
// context as is. Longer text is saved to a file and only a preview with the
// file's path is shown.
const claudeHookContextMaxChars = 10_000

// clearedNoteBudget bounds the note read. It runs after the activity report
// (hookReportBudget), and both stay under Claude's 10s hook timeout.
var clearedNoteBudget = 4 * time.Second

// clearedNoteSpillLine heads a note too long for Claude Code to show in full.
const clearedNoteSpillLine = "This note is longer than Claude Code shows at once. If you only see a preview of it, read the saved file in full before you reply."

// rebriefClearedContext gives a Claude Code agent its task back after the user
// cleared its context with /clear, a clear Tallpine cannot run itself in a
// Terminal UI. Claude keeps the appended system prompt but drops everything
// Tallpine sent as messages: the spawn brief with its publishing scope, and the
// relayed messages, findings and reports since. The SessionStart hook Claude
// runs for the clear adds the note a Chat /clear hands over with to the fresh
// context. Best effort: a failure is logged and the clear goes on without it.
func (c *commandContext) rebriefClearedContext(ctx context.Context, agent, event, sessionID string, payload []byte) {
	if domain.AgentHarness(agent) != domain.HarnessClaudeCode || event != "session-start" || sessionStartSource(payload) != "clear" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clearedNoteBudget)
	defer cancel()
	var resp apitypes.ClearedContextNoteResponse
	if err := c.getJSON(ctx, "sessions/"+url.PathEscape(sessionID)+"/cleared-context-note", &resp); err != nil {
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("read the cleared-context note: %w", err))
		return
	}
	note := strings.TrimSpace(resp.Note)
	if note == "" {
		return
	}
	if utf8.RuneCountInString(note) > claudeHookContextMaxChars {
		note = clearedNoteSpillLine + "\n\n" + note
	}
	output := sessionStartHookOutput{}
	output.HookSpecificOutput.HookEventName = "SessionStart"
	output.HookSpecificOutput.AdditionalContext = note
	if err := json.NewEncoder(c.deps.Out).Encode(output); err != nil {
		c.reportHookFailure(agent, event, sessionID, fmt.Errorf("emit the cleared-context note: %w", err))
	}
}

// sessionStartSource is why Claude Code started a session: startup, resume,
// clear, compact or fork.
func sessionStartSource(payload []byte) string {
	var p struct {
		Source string `json:"source"`
	}
	_ = json.Unmarshal(payload, &p)
	return strings.TrimSpace(p.Source)
}
