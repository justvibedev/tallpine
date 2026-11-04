package codexappserver

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/commanddetail"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func itemStarted(n notification, _ time.Time) []ports.ChatEvent {
	return normalizeItem(n.Params, false)
}

func itemCompleted(n notification, _ time.Time) []ports.ChatEvent {
	return normalizeItem(n.Params, true)
}

// normalizeItem maps an item lifecycle notification. Assistant text is a message;
// everything else is an activity with a typed payload.
func normalizeItem(params json.RawMessage, completed bool) []ports.ChatEvent {
	var p struct {
		ThreadID string     `json:"threadId"`
		TurnID   string     `json:"turnId"`
		Item     threadItem `json:"item"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	it := p.Item

	// The user's own message comes back from the provider as an item. Tallpine already
	// persisted it when the send was accepted, so re-emitting it would duplicate
	// the timeline entry.
	if it.Type == itemUserMessage {
		return nil
	}

	if it.Type == itemContextCompaction {
		if !completed {
			// A compaction in flight is not yet a fact about the conversation, and the
			// reclaim is unknown until it settles: the reduced token figure arrives
			// between the item starting and completing. Emitting on start would put a
			// row in the timeline that has to be rewritten with the real numbers.
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventCompacted,
			ProviderTurnID: p.TurnID,
			ProviderItemID: it.itemID(),
		}}
	}

	if it.Type == itemAgentMessage {
		if !completed {
			// The message row is created by the first delta; a started event
			// with no text adds nothing.
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventMessageCompleted,
			ProviderTurnID: p.TurnID,
			ProviderItemID: it.itemID(),
			Text:           deref(it.Text),
		}}
	}

	kind, summary, ok := activityFor(it)
	if !ok {
		return nil
	}

	ev := ports.ChatEvent{
		ProviderTurnID: p.TurnID,
		ProviderItemID: it.itemID(),
		ActivityKind:   kind,
		Summary:        summary,
		Detail:         activityDetail(it),
	}
	if kind == domain.ActivityKindReasoning {
		// The settled form of what the summary deltas streamed. Carried on the event so
		// the projection can replace its accumulation without decoding the detail
		// payload, and left empty when the provider settled nothing — a reasoning item
		// with an empty summary array is the DEFAULT on a build with
		// model_reasoning_summary unset, and replacing streamed text with that would
		// erase reasoning the user watched arrive.
		ev.Text = joinReasoning(it.Summary)
	}
	if completed {
		ev.Kind = ports.ChatEventActivityCompleted
		ev.ActivityStatus = domain.ActivityStatusCompleted
		if it.ExitCode != nil && *it.ExitCode != 0 {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
		if it.Type == itemError {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
		// An MCP tool reports failure in its own payload, not through an exit code.
		// Verified live: a successful call carries error: null and status
		// "completed"; the schema's McpToolCallError is what a failure sets.
		if it.Error != nil && it.Error.Message != "" {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
		if it.Success != nil && !*it.Success {
			ev.ActivityStatus = domain.ActivityStatusFailed
		}
	} else {
		ev.Kind = ports.ChatEventActivityStarted
		ev.ActivityStatus = domain.ActivityStatusRunning
	}
	return []ports.ChatEvent{ev}
}

// activityFor maps a provider item type onto an activity kind and label.
func activityFor(it threadItem) (domain.ActivityKind, string, bool) {
	switch it.Type {
	case itemCommandExecution:
		return domain.ActivityKindCommand, commandSummary(deref(it.Command)), true
	case itemFileChange:
		return domain.ActivityKindFileChange, fileChangeSummary(fileChanges(it.Changes)), true
	case itemPlan:
		return domain.ActivityKindPlan, "Updated plan", true
	case itemReasoning:
		return domain.ActivityKindReasoning, "Reasoning", true
	case itemMcpToolCall:
		return domain.ActivityKindMCPTool, mcpToolSummary(it), true
	case itemWebSearch:
		if query := strings.TrimSpace(deref(it.Query)); query != "" {
			return domain.ActivityKindCommand, "Searched the web for " + truncateLabel(query), true
		}
		return domain.ActivityKindCommand, "Searched the web", true
	case itemError:
		return domain.ActivityKindError, cmp.Or(it.Message, "Provider error"), true
	default:
		return "", "", false
	}
}

// mcpToolSummary labels an MCP tool call with the server and tool it names.
//
// `tool` and `server` are the fields the provider actually sends — verified
// against a live call: {"type":"mcpToolCall","server":"probe","tool":"slow_echo",
// "arguments":{...}}. The previous build read `toolName`, which no build sends, so
// every MCP call in the timeline read "Called tool".
func mcpToolSummary(it threadItem) string {
	tool := strings.TrimSpace(deref(it.Tool))
	server := strings.TrimSpace(deref(it.Server))
	switch {
	case tool != "" && server != "":
		return "Called " + server + "/" + tool
	case tool != "":
		return "Called " + tool
	case server != "":
		return "Called a tool on " + server
	default:
		return "Called a tool"
	}
}

// fileChangeSummary labels a file-change activity with what it touched, so a
// collapsed row says something. "Edited files" was the same label whether the
// agent renamed one path or rewrote thirty.
func fileChangeSummary(files []fileChangeDetail) string {
	switch len(files) {
	case 0:
		return "Edited files"
	case 1:
		return fileChangeVerb(files[0].Status) + " " + truncateLabel(files[0].Path)
	default:
		return fmt.Sprintf("Edited %d files", len(files))
	}
}

func fileChangeVerb(status string) string {
	switch status {
	case diffStatusAdded:
		return "Created"
	case diffStatusDeleted:
		return "Deleted"
	case diffStatusRenamed:
		return "Renamed"
	default:
		return "Edited"
	}
}

// commandSummary produces a one-line label. Codex wraps commands in a shell
// invocation (`/bin/zsh -lc '…'`); the wrapper is noise in a timeline.
func commandSummary(command string) string {
	cmd := commanddetail.UnwrapShell(strings.TrimSpace(command))
	if cmd == "" {
		return "Ran a command"
	}
	return truncateLabel(cmd)
}

// truncateLabel caps a one-line label. 120 characters is about where a timeline
// row stops being scannable.
func truncateLabel(value string) string {
	const limit = 120
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// activityDetail is the provider-neutral payload Tallpine persists for an activity.
// Provider DTOs are not passed through: only named fields Tallpine renders.
func activityDetail(it threadItem) []byte {
	detail := map[string]any{}
	if cmd := deref(it.Command); cmd != "" {
		detail["command"] = commanddetail.UnwrapShell(cmd)
		detail["rawCommand"] = cmd
	}
	if it.Cwd != nil && *it.Cwd != "" {
		detail["cwd"] = string(*it.Cwd)
	}
	if it.ProcessID != nil && *it.ProcessID != "" {
		// The PTY behind an interactive command. Needed to correlate the keystrokes
		// item/commandExecution/terminalInteraction reports with the command they
		// were typed into.
		detail["processId"] = *it.ProcessID
	}
	if out := deref(it.AggregatedOutput); out != "" {
		// The whole output in one field: past the cap only its end is kept, and the
		// cut is marked the way the streamed copy's is.
		kept, cut := commanddetail.TailChars(out, commanddetail.MaxOutputChars)
		detail["output"] = kept
		if cut {
			detail["outputTruncated"] = true
		}
		// Marked partial because the provider's own aggregation was observed to
		// drop leading output. A reader must not present it as the full record.
		detail["outputMayBePartial"] = true
		// Named so a reader can tell this from the accumulated delta stream. Only
		// the aggregate exists for a fast command, where the provider finishes
		// before it flushes a single delta; only the stream exists for a command
		// that never completes. Both are partial, for different reasons.
		detail["outputSource"] = "aggregate"
	}
	if it.ExitCode != nil {
		detail["exitCode"] = *it.ExitCode
	}
	if it.DurationMs != nil {
		detail["durationMs"] = *it.DurationMs
	}
	if text := strings.TrimSpace(deref(it.Text)); text != "" {
		detail["text"] = text
	}
	// Reasoning arrives as a list of summary sections, and the previous build read
	// none of them: it looked for a `text` field that a reasoning item does not
	// have, so every settled reasoning row was blank. Verified live: item/completed
	// carries summary: ["**Searching deferred tool in ALL_TOOLS**"].
	if summary := joinReasoning(it.Summary); summary != "" {
		detail["text"] = summary
	}
	if len(it.Changes) > 0 {
		// Tallpine's own per-file shape, not the provider's `changes` array. The provider
		// spells the change kind as an object ({"type":"update","move_path":null}),
		// so passing it through left a client with nothing it could read as a status,
		// and it was a provider DTO on Tallpine's wire besides.
		detail["files"] = fileChanges(it.Changes)
	}
	if tool := deref(it.Tool); tool != "" {
		detail["toolName"] = tool
	}
	if server := deref(it.Server); server != "" {
		detail["server"] = server
	}
	if ns := deref(it.Namespace); ns != "" {
		detail["namespace"] = ns
	}
	if len(it.Arguments) > 0 && !isJSONNull(it.Arguments) {
		// What the tool was called WITH. A tool call with no arguments shown is a
		// claim the user cannot check: "called search" says nothing about what was
		// searched for. Capped, because a tool can be handed a whole file.
		detail["arguments"] = commanddetail.TruncatedJSON(it.Arguments)
	}
	if len(it.Result) > 0 && !isJSONNull(it.Result) {
		detail["result"] = commanddetail.TruncatedJSON(it.Result)
	}
	if it.Error != nil && it.Error.Message != "" {
		detail["error"] = it.Error.Message
	}
	if it.Success != nil {
		detail["success"] = *it.Success
	}
	if query := strings.TrimSpace(deref(it.Query)); query != "" {
		detail["query"] = query
	}
	if it.Message != "" {
		detail["message"] = it.Message
	}
	if len(detail) == 0 {
		return nil
	}
	return encodeDetail(detail)
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

// joinReasoning flattens a reasoning item's summary sections into one block.
//
// The generated ThreadItem declares summary as []string and a live item/completed
// carried exactly that. Sections are joined with a blank line, matching the
// separator the streaming path inserts on summaryPartAdded, so the settled text
// and the streamed text are the same string rather than two spellings of it.
func joinReasoning(summary []string) string {
	parts := make([]string, 0, len(summary))
	for _, part := range summary {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}

// fileChangeDetail is one changed file as Tallpine puts it on the wire.
//
// Declared here with its own JSON tags rather than reusing ports.ChatDiffFile,
// which has none: marshalling that type directly would put Go field names
// ("Path", "Additions") into the payload a client reads. The port type describes an
// event in memory; this describes the wire.
type fileChangeDetail struct {
	Path      string `json:"path"`
	OldPath   string `json:"oldPath,omitempty"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Patch is this file's unified diff, so a client can render the change as soon as
	// the patch lands instead of waiting for the turn's aggregate.
	Patch string `json:"patch,omitempty"`
	// PatchTruncated reports that the patch was cut at Tallpine's cap, so a partial diff is
	// never presented as a whole one.
	PatchTruncated bool `json:"patchTruncated,omitempty"`
}

// fileChanges converts the provider's per-file patches into Tallpine's diff shape.
//
// Counts are read out of the patch text here because the provider does not send
// them. Two patch shapes arrive, both verified live: an update carries a unified
// hunk (`@@ -2 +2,2 @@\n two\n+three\n`), and an add carries the new file's
// contents with no hunk header at all.
func fileChanges(changes []codexproto.FileUpdateChange) []fileChangeDetail {
	if len(changes) == 0 {
		return nil
	}
	files := make([]fileChangeDetail, 0, len(changes))
	for _, change := range changes {
		file := fileChangeDetail{
			Path:   change.Path,
			Status: patchStatus(change.Kind.Type),
			Patch:  change.Diff,
		}
		if change.Kind.MovePath != nil && *change.Kind.MovePath != "" {
			// A move is reported as an update carrying the destination, so the path
			// Tallpine shows is where the file ended up and OldPath is where it was.
			file.Status = diffStatusRenamed
			file.OldPath = change.Path
			file.Path = *change.Kind.MovePath
		}
		// Counted from the WHOLE patch, before any truncation: the figures describe
		// the change, not how much of it Tallpine chose to store.
		file.Additions, file.Deletions = countPatchLines(change.Diff, file.Status)
		if len(file.Patch) > commanddetail.MaxPatchChars {
			file.Patch = commanddetail.Head(file.Patch, commanddetail.MaxPatchChars)
			file.PatchTruncated = true
		}
		files = append(files, file)
	}
	return files
}

// patchStatus maps the provider's change kind onto Tallpine's neutral status.
func patchStatus(kind codexproto.PatchChangeKindType) string {
	switch kind {
	case codexproto.PatchChangeKindTypeAdd:
		return diffStatusAdded
	case codexproto.PatchChangeKindTypeDelete:
		return diffStatusDeleted
	default:
		return diffStatusModified
	}
}

// countPatchLines counts added and removed lines in one file's patch.
//
// A patch with no hunk header is the whole file: an add is all additions and a
// delete is all deletions. Counting `+`/`-` prefixes in that case would count the
// file's own content as diff markers, which is how a new file full of bullet points
// would report deletions it never made.
func countPatchLines(patch, status string) (additions, deletions int) {
	if patch == "" {
		return 0, 0
	}
	if !strings.Contains(patch, "@@") {
		lines := strings.Count(strings.TrimSuffix(patch, "\n"), "\n") + 1
		switch status {
		case diffStatusDeleted:
			return 0, lines
		case diffStatusAdded:
			return lines, 0
		default:
			return 0, 0
		}
	}
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk:
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

// turnStateFrom maps a provider turn status onto Tallpine's turn state. An unknown
// status is failed rather than completed: claiming success Tallpine did not observe is
// worse than reporting an honest unknown failure.
func turnStateFrom(status string) domain.TurnState {
	switch status {
	case string(codexproto.TurnStatusCompleted):
		return domain.TurnStateCompleted
	case string(codexproto.TurnStatusInterrupted), "cancelled", "canceled":
		return domain.TurnStateInterrupted
	case string(codexproto.TurnStatusInProgress), "in_progress", "running", "active":
		return domain.TurnStateRunning
	case "queued", "pending":
		return domain.TurnStateQueued
	default:
		return domain.TurnStateFailed
	}
}

// rawID stringifies a JSON-RPC id, which the protocol allows to be either a
// number or a string. `null`, absent, and empty all yield "".
func rawID(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	return strings.Trim(s, `"`)
}

// deref reads an optional string the generated types declare as a pointer.
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// encodeDetail marshals a neutral payload, returning nil rather than a partial
// document if it cannot be encoded.
func encodeDetail(detail map[string]any) []byte {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

func truncateForLog(raw []byte) string {
	const maxLogBytes = 400
	s := string(raw)
	if len(s) > maxLogBytes {
		return s[:maxLogBytes] + "…"
	}
	return s
}
