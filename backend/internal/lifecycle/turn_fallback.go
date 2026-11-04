package lifecycle

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// terminalFallback uses the native Codex turn ID. Claude prompt_id can refer
// to an executing queue anchor and is not a unique completion identity; that
// path keeps the legacy observer until its hook supplies an exact native ID.
func terminalFallback(previous, next domain.SessionRecord, signal ports.ActivitySignal, now time.Time) (domain.TurnFallback, bool) {
	f := domain.TurnFallback{}
	if next.Kind != domain.KindWorker || domain.NormalizeSessionMode(next.Mode) != domain.SessionModeTUI || next.Harness != domain.HarnessCodex ||
		signal.SubagentID != "" || signal.ProviderTurnID == "" || signal.LaunchID == "" || signal.AgentSessionID == "" || signal.IntentionalStop {
		return f, false
	}
	switch signal.NativeEntrypoint {
	case "sdk-cli", "sdk-ts", "sdk-py":
		return f, false
	}
	if signal.Event != "user-prompt-submit" && signal.Event != "stop" {
		return f, false
	}
	if signal.Event == "stop" && (staleTerminalStop(previous, signal) || next.Activity.State != domain.ActivityIdle || (signal.RunningSubagentIDs != nil && len(*signal.RunningSubagentIDs) > 0)) {
		return f, false
	}
	f = domain.TurnFallback{SessionID: next.ID, Mode: domain.SessionModeTUI, Generation: signal.LaunchID, NativeID: signal.AgentSessionID, TurnID: signal.ProviderTurnID}
	if signal.Event == "stop" {
		f.CompletedAt = timeOr(signal.Timestamp, now)
		f.Excerpt = signal.FinalAssistantMessage
	}
	return f, true
}
