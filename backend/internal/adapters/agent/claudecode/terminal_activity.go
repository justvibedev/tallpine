package claudecode

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/terminalui"
)

// DetectTerminalActivity reports idle only when Claude's rendered surface
// positively shows an idle composer: no active spinner/interrupt chrome and no
// pending provider decision frame. Anything else is not authoritative.
//
// Claude Code's durable activity is hook-driven, and a turn that dies without
// emitting its Stop hook (auth expiry, CLI crash, network loss) leaves the
// session stuck active forever. This capability is what opts the adapter into
// the activity observer's stale-active reconciliation, which screen-proves
// idle after the hook state has been quiet past its staleness limit. Claude
// intentionally does not implement ContinuousTerminalActivityDetector: hooks
// stay authoritative while they flow, and the composer draft state must never
// promote or demote sticky states on its own.
//
// A login expiry reads as waiting_input only when it is the turn's latest
// provider response and the surface is idle. The captured pane includes
// scrollback, so a stale "Login expired" line (the user ran /login and work
// resumed) or the same words in tool output would otherwise flip a working
// session to waiting_input, which tool hooks cannot clear until the turn ends.
func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	if p.InspectTerminalSurface(output).Work != ports.TerminalSurfaceWorkIdle {
		return "", false
	}
	if claudeLatestResponseIsLoginExpired(output) {
		return domain.ActivityWaitingInput, true
	}
	return domain.ActivityIdle, true
}

// claudeLatestResponseIsLoginExpired reports whether the last provider
// response block above the current composer is Claude's login-expired notice.
func claudeLatestResponseIsLoginExpired(output string) bool {
	lines := terminalui.PlainTerminalLines(output)
	prompt := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "❯") {
			prompt = i
			break
		}
	}
	for i := prompt - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "⏺") {
			continue
		}
		line = strings.ToLower(line)
		return strings.Contains(line, "login expired") && strings.Contains(line, "run /login")
	}
	return false
}
