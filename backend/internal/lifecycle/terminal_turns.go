package lifecycle

import (
	"cmp"
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type terminalTurnRecorder interface {
	RecordTerminalTurnRequest(context.Context, domain.TerminalTurn) error
	RecordTerminalTurnResponse(context.Context, domain.SessionID, string, time.Time) error
}

// recordTerminalTurn keeps Tallpine's own record of a Terminal UI turn: the request
// from the prompt hook and the agent's final message from the stop hook. Chat
// turns live in the conversation tables, so the caller records only in terminal
// mode. A failure here must never reject the activity signal itself.
func (m *Manager) recordTerminalTurn(ctx context.Context, rec domain.SessionRecord, s ports.ActivitySignal, now time.Time) {
	recorder, ok := m.store.(terminalTurnRecorder)
	if !ok {
		return
	}
	at := timeOr(s.Timestamp, now)
	var err error
	switch s.Event {
	case "user-prompt-submit":
		request := strings.TrimSpace(s.TurnUserRequest)
		coordination := s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination
		if coordination || request == "" {
			// Tallpine's own message opened this turn, or the hook carried no text.
			// Close any turn still open so the coming stop cannot answer an
			// earlier request.
			err = recorder.RecordTerminalTurnResponse(ctx, rec.ID, "", at)
			if err != nil || request == "" {
				break
			}
			// A delivered worker report or session send carries its text: it
			// opens a turn of its own, so the agent's answer to it is kept,
			// as Chat keeps it. Tallpine's handoff and continuation prompts carry none.
		}
		err = recorder.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
			SessionID: rec.ID, Harness: rec.Harness, UserRequest: request, RequestedAt: at,
			NativeSessionID: cmp.Or(s.AgentSessionID, rec.Metadata.AgentSessionID),
		})
	case "stop":
		// The turn is over even when the hook carries no text. Closing it
		// keeps the next request from piling onto this one.
		if s.SubagentID == "" && !staleTerminalStop(rec, s) {
			err = recorder.RecordTerminalTurnResponse(ctx, rec.ID, strings.TrimSpace(s.FinalAssistantMessage), at)
		}
	}
	if err != nil {
		slog.Default().Warn("lifecycle: record terminal turn", "session", rec.ID, "event", s.Event, "error", err)
	}
}

// staleTerminalStop reports a Stop that belongs to an earlier turn than the
// one the open terminal row holds: it is older than the latest prompt, or it
// names a different turn than the one that prompt opened. Recording it would
// close the newer request with an older answer, so it is left for the
// lifecycle checkpoint to reject.
func staleTerminalStop(rec domain.SessionRecord, s ports.ActivitySignal) bool {
	md := rec.Metadata
	if !s.Timestamp.IsZero() && !md.LatestUserPromptAt.IsZero() && s.Timestamp.Before(md.LatestUserPromptAt) {
		return true
	}
	return s.ProviderTurnID != "" && md.ConversationCheckpointState == domain.ConversationCheckpointPrompt &&
		md.ConversationCheckpointTurnID != "" && md.ConversationCheckpointTurnID != s.ProviderTurnID
}

// MaxLastAssistantMessageBytes bounds the stored display copy of a turn's
// final message.
const MaxLastAssistantMessageBytes = 4 << 10

type lastAssistantRecorder interface {
	RecordLastAssistantMessage(context.Context, domain.SessionID, string, time.Time) error
}

// recordLastAssistantMessage keeps a display copy of a main turn's final
// message: a Terminal UI stop hook, or a Chat controller's turn completion. It
// is never a replay checkpoint (that is LatestAssistantUpdate, which Claude's
// native boundary deliberately leaves empty), and a failure never rejects the
// signal.
func (m *Manager) recordLastAssistantMessage(ctx context.Context, rec domain.SessionRecord, s ports.ActivitySignal, mode domain.SessionMode, now time.Time) {
	recorder, ok := m.store.(lastAssistantRecorder)
	if !ok || s.SubagentID != "" {
		return
	}
	switch {
	case mode == domain.SessionModeTUI && s.Event == "stop" && !staleTerminalStop(rec, s):
	case mode == domain.SessionModeChat && s.Event == "chat.turn.completed":
	default:
		return
	}
	message := truncateUTF8(strings.TrimSpace(s.FinalAssistantMessage), MaxLastAssistantMessageBytes)
	if message == "" {
		return
	}
	if err := recorder.RecordLastAssistantMessage(ctx, rec.ID, message, timeOr(s.Timestamp, now)); err != nil {
		slog.Default().Warn("lifecycle: record last assistant message", "session", rec.ID, "event", s.Event, "error", err)
	}
}

func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
