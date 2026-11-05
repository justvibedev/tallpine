package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/hookutil"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var _ ports.TranscriptTurnFailureDetector = (*Plugin)(nil)

// kiroTurnTailWindow bounds how much of the log's end is read for the last
// turn boundary. turn_end is written after the turn's tool output, so it is
// the newest small record; a window without a boundary reports nothing.
const kiroTurnTailWindow = 256 << 10

// LastTurnFailure reads the end of a Kiro v3 session log. Verified against
// kiro-cli 3.0.0: when the model request fails (an unavailable model, a usage
// limit, a server error) Kiro fires neither Stop nor SessionEnd, and no hook
// carries the error; it only records turn_end with stopReason "error" (and a
// usage_summary with status "failed"). An Esc interrupt is stopReason
// "cancelled". Only the newest turn boundary counts: a later user or
// turn_start record means a newer turn is running, so an old failure is history.
func (p *Plugin) LastTurnFailure(ctx context.Context, transcriptPath string) (ports.TurnFailure, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.TurnFailure{}, false, err
	}
	path := strings.TrimSpace(transcriptPath)
	// The path comes from the session record; read only what is a Kiro log.
	if !filepath.IsAbs(path) || filepath.Base(path) != sessionLogFile {
		return ports.TurnFailure{}, false, nil
	}
	tail, err := hookutil.ReadJSONLTail(path, kiroTurnTailWindow)
	if errors.Is(err, fs.ErrNotExist) {
		return ports.TurnFailure{}, false, nil
	}
	if err != nil {
		return ports.TurnFailure{}, false, err
	}
	failure, ok := lastKiroTurnFailure(tail)
	return failure, ok, nil
}

func lastKiroTurnFailure(records []byte) (ports.TurnFailure, bool) {
	for len(records) > 0 {
		records = bytes.TrimRight(records, "\n")
		start := bytes.LastIndexByte(records, '\n') + 1
		line := records[start:]
		records = records[:start]
		failure, boundary, failed := kiroTurnBoundary(line)
		if boundary {
			return failure, failed
		}
	}
	return ports.TurnFailure{}, false
}

func kiroTurnBoundary(line []byte) (failure ports.TurnFailure, boundary, failed bool) {
	if !bytes.Contains(line, []byte(`"turn_end"`)) && !bytes.Contains(line, []byte(`"turn_start"`)) &&
		!bytes.Contains(line, []byte(`"type":"user"`)) {
		return ports.TurnFailure{}, false, false
	}
	var record struct {
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Type       string `json:"type"`
			StopReason string `json:"stopReason"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil {
		return ports.TurnFailure{}, false, false
	}
	switch record.Payload.Type {
	case "user", "turn_start":
		return ports.TurnFailure{}, true, false
	case "turn_end":
	default:
		return ports.TurnFailure{}, false, false
	}
	reason := record.Payload.StopReason
	if reason == "end_turn" || reason == "" {
		return ports.TurnFailure{}, true, false
	}
	// A failed end without a readable time is not reported: dating it now could
	// make an old failure look newer than the current prompt.
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return ports.TurnFailure{}, true, false
	}
	if reason == "cancelled" {
		return ports.TurnFailure{At: at, Interrupted: true}, true, true
	}
	return ports.TurnFailure{At: at, Message: "Kiro turn ended: " + reason}, true, true
}
