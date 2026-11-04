package usage

import (
	"encoding/json"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type jsonlRecord struct {
	Data   []byte
	Offset int64
}

type parseResult struct {
	Events                 []domain.ModelUsageEvent
	Cursor                 domain.SourceCursorState
	newCodexChild          bool
	pendingCodexSpawnCalls int
	err                    error
}

func parseRecordsWithState(
	source domain.UsageSourceContext,
	records []jsonlRecord,
	nextOffset int64,
	now time.Time,
	state *parserStateEnvelope,
) parseResult {
	result := parseResult{Cursor: cursorFromSource(source.Source, nextOffset, now)}
	switch source.Source.Kind {
	case domain.UsageSourceClaudeMain, domain.UsageSourceClaudeSubagent:
		parseClaude(source, records, state.Claude, &result)
	case domain.UsageSourceCodexRollout:
		parseCodex(source, records, state.Codex, &result)
		result.pendingCodexSpawnCalls = len(state.Codex.PendingSpawnCallIDs)
	case domain.UsageSourceKimiWire:
		parseKimi(source, records, &result)
	default:
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorUnsupportedSourceFormat
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		result.err = fmt.Errorf("encode parser state: %w", err)
		return result
	}
	result.Cursor.ParserStateJSON = string(encoded)
	return result
}

func cursorFromSource(source domain.UsageSourceRecord, nextOffset int64, now time.Time) domain.SourceCursorState {
	return domain.SourceCursorState{
		ByteOffset:   nextOffset,
		State:        domain.UsageSourceActive,
		FailureCount: 0,
		AnomalyCount: source.AnomalyCount,
		UpdatedAt:    now,
	}
}
