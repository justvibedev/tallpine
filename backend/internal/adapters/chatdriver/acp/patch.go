package acp

import (
	"encoding/json"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/commanddetail"
)

func acpFilePatch(path string, oldText *string, newText string) string {
	oldLines, newLines := acpPatchLines(oldText, newText)
	return acpPatch(path, oldLines, newLines)
}

func acpPatchLines(oldText *string, newText string) (oldLines, newLines []string) {
	oldLines = []string{}
	if oldText != nil {
		oldLines = strings.Split(strings.ReplaceAll(*oldText, "\r\n", "\n"), "\n")
	}
	return oldLines, strings.Split(strings.ReplaceAll(newText, "\r\n", "\n"), "\n")
}

// cappedACPPatch is the patch for a file past the per-file cap. The full form lists
// every old line before any new one, so cutting it would keep only removals and read
// as though the file had been emptied. Lines both sides share at the start and the
// end are dropped first; if the rest still does not fit, the removed and the added
// lines share the budget, each side keeping its first lines. Reports whether any of
// the change itself was cut.
func cappedACPPatch(path string, oldText *string, newText string) (string, bool) {
	oldLines, newLines := acpPatchLines(oldText, newText)
	for len(oldLines) > 0 && len(newLines) > 0 && oldLines[0] == newLines[0] {
		oldLines, newLines = oldLines[1:], newLines[1:]
	}
	for len(oldLines) > 0 && len(newLines) > 0 && oldLines[len(oldLines)-1] == newLines[len(newLines)-1] {
		oldLines, newLines = oldLines[:len(oldLines)-1], newLines[:len(newLines)-1]
	}
	// The full path is stored separately; reserve room for both sides of the change.
	if len(path) > commanddetail.MaxPatchChars/4 {
		path = commanddetail.Head(path, commanddetail.MaxPatchChars/4-3) + "..."
	}
	if patch := acpPatch(path, oldLines, newLines); len(patch) <= commanddetail.MaxPatchChars {
		return patch, false
	}
	budget := commanddetail.MaxPatchChars - len(acpPatch(path, nil, nil))
	// Half each, and whatever one side does not need goes to the other.
	oldBudget := max(min(patchLinesSize(oldLines), budget/2), budget-patchLinesSize(newLines))
	return acpPatch(path, keepPatchLines(oldLines, oldBudget), keepPatchLines(newLines, budget-oldBudget)), true
}

// patchLinesSize is what lines add to a patch: each line, its marker and a newline.
func patchLinesSize(lines []string) int {
	size := 0
	for _, line := range lines {
		size += len(line) + 2
	}
	return size
}

// keepPatchLines keeps the first lines that fit in budget bytes of patch. A first
// line that alone does not fit is cut on a character boundary.
func keepPatchLines(lines []string, budget int) []string {
	kept := 0
	for _, line := range lines {
		if len(line)+2 > budget {
			break
		}
		budget -= len(line) + 2
		kept++
	}
	if kept == 0 && len(lines) > 0 && budget > 2 {
		return []string{commanddetail.Head(lines[0], budget-2)}
	}
	return lines[:kept]
}

func acpPatch(path string, oldLines, newLines []string) string {
	lines := make([]string, 0, 2+len(oldLines)+len(newLines))
	lines = append(lines, "--- "+path, "+++ "+path)
	for _, line := range oldLines {
		lines = append(lines, "-"+line)
	}
	for _, line := range newLines {
		lines = append(lines, "+"+line)
	}
	return strings.Join(lines, "\n")
}

// cappedPayload keeps a tool payload (its input, or content its row does not
// render) only while it fits the tool payload cap, as the Codex driver does for
// MCP arguments and results.
func cappedPayload(raw any) any {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return commanddetail.TruncatedJSON(encoded)
}

// imageStub stands in for an image block. Its base64 data would be stored as
// text and printed as text when the row is expanded; bytes is its length.
func imageStub(mimeType any, data string) map[string]any {
	return map[string]any{"type": "image", "mimeType": mimeType, "bytes": len(data)}
}

// contentWithoutImageData keeps a tool's content blocks, text included, and
// replaces each image block with imageStub.
func contentWithoutImageData(content []acpsdk.ToolCallContent) []any {
	if content == nil {
		return nil
	}
	out := make([]any, len(content))
	for i, item := range content {
		if item.Content != nil && item.Content.Content.Image != nil {
			image := item.Content.Content.Image
			out[i] = map[string]any{"type": "content", "content": imageStub(image.MimeType, image.Data)}
			continue
		}
		out[i] = item
	}
	return out
}

// withoutImageData replaces every image block in a decoded JSON value with
// imageStub: MCP's {type:"image", data, mimeType} and the Anthropic
// {type:"image", source:{type:"base64", media_type, data}} that
// claude-agent-acp passes through as rawOutput.
func withoutImageData(value any) any {
	switch v := value.(type) {
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = withoutImageData(item)
		}
		return out
	case map[string]any:
		if v["type"] == "image" {
			if data, ok := v["data"].(string); ok {
				return imageStub(v["mimeType"], data)
			}
			if source, ok := v["source"].(map[string]any); ok {
				if data, ok := source["data"].(string); ok {
					return imageStub(source["media_type"], data)
				}
			}
		}
		out := make(map[string]any, len(v))
		for key, item := range v {
			out[key] = withoutImageData(item)
		}
		return out
	}
	return value
}

// toolOutputText translates ACP's provider-defined rawOutput into Tallpine's neutral
// command-detail contract, where output is always text. ACP deliberately permits
// any JSON value here; OpenCode, for example, wraps the text as
// {"output":"...","metadata":{...}}. Persisting that object unchanged makes the
// typed frontend contract untrue and crashes text-only renderers such as ANSI
// cleanup.
func toolOutputText(raw any) string {
	switch value := raw.(type) {
	case nil:
		return ""
	case string:
		return value
	case map[string]any:
		for _, key := range []string{"output", "text", "error"} {
			if text := toolOutputText(value[key]); text != "" {
				return text
			}
		}
		if text := toolOutputText(value["metadata"]); text != "" {
			return text
		}
	}

	encoded, err := json.Marshal(raw)
	if err != nil || string(encoded) == "null" {
		return ""
	}
	return string(encoded)
}

// rawCommandFromInput extracts the verbatim shell command from a tool's
// provider-defined rawInput so execute activities can carry Tallpine's neutral command
// detail without making the provider object itself part of that contract.
// Providers wrap the command differently (claude-code Bash uses
// {"command": "..."}); anything unrecognizable stays empty rather than putting
// a provider DTO on the wire as if it were the command.
func rawCommandFromInput(raw any) string {
	value, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"command", "cmd"} {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}
