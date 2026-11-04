package acp

import (
	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// sessionModeFor binds the driver's mode mapping to a conversation's decision.
func (d *Driver) sessionModeFor(policy agentpolicy.Decision) func(ports.PermissionMode) string {
	if d.cfg.SessionMode == nil {
		return nil
	}
	return func(mode ports.PermissionMode) string { return d.cfg.SessionMode(policy, mode) }
}

// boundPermissionPolicy is a PermissionPolicy bound to one conversation's decision.
type boundPermissionPolicy func(ports.PermissionMode, acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool)

// permissionPolicyFor binds the driver's permission policy to a conversation's decision.
func (d *Driver) permissionPolicyFor(policy agentpolicy.Decision) boundPermissionPolicy {
	if d.cfg.PermissionPolicy == nil {
		return nil
	}
	return func(mode ports.PermissionMode, params acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
		return d.cfg.PermissionPolicy(policy, mode, params)
	}
}
