package controllers

import (
	"tallpine.eu/tallpine/backend/internal/service/systemcheck"
	"tallpine.eu/tallpine/backend/internal/service/systeminstall"
)

// SystemRequirementsResponse is the body of GET /api/v1/system/requirements.
type SystemRequirementsResponse = systemcheck.Report

// GitHubAuthRequirementResponse is the advisory GitHub credential probe.
type GitHubAuthRequirementResponse = systemcheck.Requirement

// InstallTargetParam is the {target} path parameter for /system/install routes.
type InstallTargetParam struct {
	Target string `path:"target" enum:"tmux,gh,claude,codex,kiro,cloudflared" description:"Install target identifier: tmux, gh, claude, codex, kiro, or cloudflared."`
}

// StartInstallResponse is the body of POST /api/v1/system/install/{target} (202).
type StartInstallResponse = systeminstall.Job

// InstallStatusResponse is the body of GET /api/v1/system/install/{target}.
type InstallStatusResponse = systeminstall.Job

// AgentInstallResponse is shared by the agent harness start and status routes.
type AgentInstallResponse = systeminstall.Job

// AgentUpdateAdvisoryQuery allows an explicit user action to bypass cached version evidence.
type AgentUpdateAdvisoryQuery struct {
	Refresh bool `query:"refresh,omitempty" description:"Re-probe the installed binary, ownership and latest release instead of reusing cached advisory evidence."`
}

// StartAgentInstallRequest selects one method returned by the installer
// catalog. The daemon still owns the argv behind the method id.
type StartAgentInstallRequest struct {
	Method          string                       `json:"method,omitempty" description:"Server-issued installation method id. Omit to use the recommended viable method for an install or reinstall, and the method that owns the installed copy for an update."`
	Operation       systeminstall.AgentOperation `json:"operation,omitempty" enum:"install,reinstall,update" description:"Requested operation. Defaults to install for older clients."`
	ExpectedVersion string                       `json:"expectedVersion,omitempty" description:"For updates, the available version explicitly approved by the user. The update must reach it. It is installed only when it lies between the installed version and the latest release Tallpine found; otherwise that latest release is installed. A version at or below the installed one, or one sent with another operation, is rejected with INVALID_UPDATE_VERSION."`
}

// AgentInstallJobsResponse hydrates Settings with the latest durable job for
// every harness that has been installed or verified.
type AgentInstallJobsResponse struct {
	Jobs []systeminstall.Job `json:"jobs"`
}

// InstallIDParam is the {installId} path parameter for mobile-device roster
// routes.
type InstallIDParam struct {
	InstallID string `path:"installId" description:"The device's stable install id."`
}
