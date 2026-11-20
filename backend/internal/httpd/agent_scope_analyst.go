package httpd

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// authorizeAgent is authorizeCross after the rules only a context analyst
// has: it may not report or message anyone, its output is files only (B827).
func (s *agentScopes) authorizeAgent(r *http.Request, agent requestscope.Agent) (reason, cross string) {
	if agent.ContextAnalyst && analystDenied(r, agent.SessionID) {
		return "the context analyst only writes files", ""
	}
	return s.authorizeCross(r, agent)
}

// analystDenied reports whether r changes anything. A context analyst's
// output is files only, so every write is refused (reports, messages, asks,
// spawns, cues, intents) except its own harness hook, which records the
// session's activity and usage.
func analystDenied(r *http.Request, own domain.SessionID) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	return r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/"+string(own)+"/activity"
}
