package kiro

import (
	"context"
	"fmt"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/nativeconfig"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var (
	_ ports.AgentContinuationCapabilityProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionConfigProvider    = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber            = (*Plugin)(nil)
)

// ContinuationCapabilities reports verified kiro-cli 3.0.0 behavior: `chat
// --v3 --agent tallpine --resume-id <id>` continues the conversation with its
// context under the same id, and the --agent profile re-applies current
// instructions on resume. kiro-cli has no flag to choose a fresh session's id,
// so Kiro assigns it and Tallpine captures it from the SessionStart hook.
func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{
		FreshNativeSessionID: ports.FreshNativeSessionIDProviderAssigned,
	}
}

// NativeSessionConfigDir returns Kiro's state root (~/.kiro under the launch's
// HOME); kiro-cli exposes no environment override for it.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.HomeDir(env, ".kiro")
	if err != nil {
		return "", fmt.Errorf("kiro: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession checks for the session directory kiro-cli keeps at
// <config>/sessions/<cwd-hash>/sess_<id>/. session.json exists from session
// creation, so a hit is authoritative; a miss is Unknown because --resume-id can
// still resume ids stored elsewhere.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if err := ctx.Err(); err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	id := canonicalKiroSessionID(ref.NativeSessionID)
	if id == "" {
		return ports.NativeSessionAvailabilityUnknown, fmt.Errorf("kiro: invalid native session id")
	}
	if findSessionDirWith(filepath.Join(ref.ConfigDir, "sessions"), id, "session.json") != "" {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	// Kiro v3 also resumes ids this store does not hold (v2-created sessions,
	// see NativeConversationExists), so absence is not proof of loss.
	return ports.NativeSessionAvailabilityUnknown, nil
}
