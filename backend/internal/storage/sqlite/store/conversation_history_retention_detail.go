package store

import (
	"bytes"
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// prunableDetailFields maps each long detail field a history prune may shorten
// to the flag the timeline reads to say it was cut. A field is listed only when
// the client can mark it as cut (frontend/src/renderer/types/conversation/
// details.ts); anything else, including file oldText/newText, is left whole.
var prunableDetailFields = map[string]string{
	"output":        "outputTruncated",
	"text":          "textTruncated",
	"terminalInput": "terminalInputTruncated",
	"patchOutput":   "patchOutputTruncated",
	"progress":      "progressTruncated",
}

// prunableFileFields is prunableDetailFields for each entry of a file change's
// files list.
var prunableFileFields = map[string]string{"patch": "patchTruncated"}

// prunableActivityKind reports the activity kinds whose detail and streamed
// columns hold provider output with truncation flags. Every other kind, system
// rows such as user steers included, carries user-authored or exact data and
// is never rewritten.
func prunableActivityKind(kind domain.ActivityKind) bool {
	switch kind {
	case domain.ActivityKindCommand, domain.ActivityKindReasoning,
		domain.ActivityKindFileChange, domain.ActivityKindMCPTool:
		return true
	}
	return false
}

// prunedActivityDetail shortens the listed long fields of a prunable activity's
// detail to a head of limit bytes and sets each one's truncation flag. Per file
// of a file change only patch is cut, with its patchTruncated flag. The
// rewritten detail records the digest of the original under
// domain.ActivityDetailOriginalDigestKey, so native-history reconciliation
// still matches it to a replay. Detail that is not a JSON object, or that is an
// event row, is left alone. It reports false when nothing was shortened.
func prunedActivityDetail(kind domain.ActivityKind, raw string, limit int) (string, bool) {
	if !prunableActivityKind(kind) || len(raw) <= limit || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return raw, false
	}
	// UseNumber keeps large integers (ids, byte counts) exact through the
	// round trip instead of turning them into float64.
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var detail map[string]any
	if err := dec.Decode(&detail); err != nil || detail == nil {
		return raw, false
	}
	if _, event := detail["event"]; event {
		return raw, false
	}
	cut := cutFields(detail, prunableDetailFields, limit)
	if files, ok := detail["files"].([]any); ok {
		for _, file := range files {
			if entry, ok := file.(map[string]any); ok && cutFields(entry, prunableFileFields, limit) {
				cut = true
			}
		}
	}
	if !cut {
		return raw, false
	}
	detail[domain.ActivityDetailOriginalDigestKey] = domain.ActivityDetailDigest([]byte(raw))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(detail); err != nil {
		return raw, false
	}
	return strings.TrimSuffix(buf.String(), "\n"), true
}

// cutFields shortens each listed string field longer than limit and sets its
// flag.
func cutFields(detail map[string]any, fields map[string]string, limit int) bool {
	cut := false
	for field, flag := range fields {
		text, ok := detail[field].(string)
		if !ok {
			continue
		}
		if head, shortened := textutil.HeadBytes(text, limit); shortened {
			detail[field], detail[flag] = head, true
			cut = true
		}
	}
	return cut
}
