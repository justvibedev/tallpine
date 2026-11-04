package apitypes

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// SessionActivityResponse is the step in flight and the last few finished.
type SessionActivityResponse struct {
	Current *SessionStepResponse `json:"current,omitempty"`
	// Recent is newest first, at most a handful.
	Recent []SessionStepResponse `json:"recent"`
}

// SessionMemoryProcessResponse is one process in a session's runtime tree.
type SessionMemoryProcessResponse struct {
	PID        int     `json:"pid"`
	PPID       int     `json:"ppid"`
	RSSBytes   uint64  `json:"rssBytes" minimum:"0"`
	CPUPercent float64 `json:"cpuPercent" minimum:"0" description:"Share of one core used since the previous sample; zero on the first."`
	Command    string  `json:"command"`
}

// SessionMemoryResponse is the resident memory of one live session's process
// tree at sampledAt. Sessions without a live runtime are absent, never zero.
type SessionMemoryResponse struct {
	SessionID    domain.SessionID               `json:"sessionId"`
	RSSBytes     uint64                         `json:"rssBytes" minimum:"0" description:"Resident set size summed over the runtime process tree."`
	ProcessCount int                            `json:"processCount" minimum:"0"`
	CPUPercent   float64                        `json:"cpuPercent" minimum:"0" description:"Share of one core the whole tree used since the previous sample; zero on the first."`
	SampledAt    time.Time                      `json:"sampledAt"`
	Processes    []SessionMemoryProcessResponse `json:"processes"`
	// Activity is what the agent has been doing, from its tool hooks. Absent
	// for harnesses that emit none (the process tree is all there is).
	Activity *SessionActivityResponse `json:"activity,omitempty"`
}

// SessionStepResponse is one tool call the agent made: which kind of tool
// and when. Deliberately not what it was given; the window is a glance.
type SessionStepResponse struct {
	Tool      string     `json:"tool"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty" description:"Absent while the tool is still running."`
	Failed    bool       `json:"failed"`
}
