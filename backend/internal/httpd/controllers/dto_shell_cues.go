package controllers

import (
	"time"
)

// ShellTerminalHandleIDParam is the {handleId} path parameter for shell
// terminal routes. It is the runtime handle the terminal mux attaches to, not
// a session id.
type ShellTerminalHandleIDParam struct {
	HandleID string `path:"handleId" description:"Shell terminal runtime handle identifier."`
}

// OpenShellTerminalRequest is the body of POST /api/v1/shell-terminals.
type OpenShellTerminalRequest struct {
	ProjectID     string `json:"projectId,omitempty" description:"Project whose root the shell starts in. Omitted opens the shell in the daemon data dir."`
	SessionID     string `json:"sessionId,omitempty" description:"Agent session the shell is scoped to, so it appears only in that session's tab strip. Omitted makes it a standalone shell."`
	Shell         string `json:"shell,omitempty" description:"Windows shell selector: auto, git-bash, pwsh, powershell, cmd, or a custom executable path. Ignored on macOS and Linux."`
	StartOnAttach bool   `json:"startOnAttach,omitempty" description:"Start the shell when the requesting client attaches with its terminal grid instead of immediately, so the shell starts at the size that client shows. Only for clients that attach as a sized viewer; omitted starts the shell immediately at the default grid."`
	Title         string `json:"title,omitempty" description:"Tab title for the new shell, for a client that already shows its tab. Trimmed; omitted or empty numbers it after the existing shells (Terminal N)."`
}

// UpdateShellTerminalRequest is the body of PATCH /api/v1/shell-terminals/{handleId}.
type UpdateShellTerminalRequest struct {
	Title string `json:"title" description:"New tab title for the shell terminal. Trimmed; must be non-empty."`
}

// ShellTerminalResponse is one standalone shell terminal. HandleID is what the
// client opens on the terminal mux, exactly as it would a session's pane.
type ShellTerminalResponse struct {
	HandleID   string    `json:"handleId"`
	ProjectID  string    `json:"projectId,omitempty"`
	SessionID  string    `json:"sessionId,omitempty"`
	WorkingDir string    `json:"workingDir"`
	Title      string    `json:"title"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ListShellTerminalsResponse is the body of GET /api/v1/shell-terminals.
type ListShellTerminalsResponse struct {
	ShellTerminals []ShellTerminalResponse `json:"shellTerminals"`
}

// ShellTerminalEnvelope is the { shellTerminal } response body for shell
// terminal mutations.
type ShellTerminalEnvelope struct {
	ShellTerminal ShellTerminalResponse `json:"shellTerminal"`
}

// CueIDParam is the {cueId} path parameter of the /cues/{cueId} routes.
type CueIDParam struct {
	CueID string `path:"cueId" description:"Cue identifier."`
}

// CueProjectIDParam is the {projectId} path parameter of the project-scoped
// /projects/{projectId}/cues routes.
type CueProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project whose cues are listed or extended."`
}

// ListCuesResponse is the body of GET /api/v1/projects/{projectId}/cues.
type ListCuesResponse struct {
	Cues []CueResponse `json:"cues"`
}

// InvokeCueRequest is the optional body of POST /api/v1/cues/{cueId}/invoke.
type InvokeCueRequest struct {
	SessionID string `json:"sessionId,omitempty" description:"Optional exact session target. Agent cues message it; command cues use its worktree. Omit it to spawn an agent worker or run a command in the project root. A supplied id must be non-blank and compatible, and never falls back to a replacement worker. Invocation body limit: 4 KiB."`
	Shell     string `json:"shell,omitempty" description:"Desktop shell selection used only for command cues."`
}

// InvokeCueResponse is the body of POST /api/v1/cues/{cueId}/invoke.
type InvokeCueResponse struct {
	Kind          string                 `json:"kind" enum:"agent,command" description:"Invocation kind."`
	SessionID     string                 `json:"sessionId,omitempty" description:"For agent cues, the session that received the prompt or newly spawned worker."`
	ShellTerminal *ShellTerminalResponse `json:"shellTerminal,omitempty" description:"For command cues, the normal shell terminal that received the command."`
}

// CueEnvelope is the { cue } response body for cue reads and mutations.
type CueEnvelope struct {
	Cue CueResponse `json:"cue"`
}
