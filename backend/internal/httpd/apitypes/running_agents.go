package apitypes

import "tallpine.eu/tallpine/backend/internal/domain"

// RunningAgentsResponse is the body of GET /api/v1/sessions/running-agents.
type RunningAgentsResponse struct {
	// SessionIDs lists the sessions whose agent is still running: the live
	// sessions whose agent has not exited or been put to sleep.
	SessionIDs []domain.SessionID `json:"sessionIds"`
}

// StopAllAgentsResponse is the body of POST /api/v1/sessions/running-agents/stop.
type StopAllAgentsResponse struct {
	OK bool `json:"ok"`
	// Stopped lists the sessions whose agent was stopped. Each session stays
	// and runs again once its agent is resumed.
	Stopped []domain.SessionID `json:"stopped"`
	// Failures holds one message per agent that could not be stopped.
	Failures []string `json:"failures"`
}
