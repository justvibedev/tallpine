package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/nativeconfig"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var (
	_ ports.AgentContinuationCapabilityProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionConfigProvider    = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber            = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator              = (*Plugin)(nil)
)

// ContinuationCapabilities reports that Codex accepts current developer
// instructions on fresh launch and `codex resume`. Codex itself assigns fresh
// conversation ids, which Tallpine captures through SessionStart hooks.
func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{
		FreshNativeSessionID: ports.FreshNativeSessionIDProviderAssigned,
	}
}

// NativeSessionConfigDir returns the exact CODEX_HOME used by the invocation,
// falling back to Codex's standard ~/.codex state root.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.CodexHome(env)
	if err != nil {
		return "", fmt.Errorf("codex: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession checks only Codex's active sessions directory. An
// archived transcript remains useful handoff context but is not treated as a
// resumable active conversation.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	sessionID, err := validateCodexNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	_, ok, err := findRollout(ctx, filepath.Join(ref.ConfigDir, "sessions"), sessionID, true)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// LocateTranscript finds Codex's rollout JSONL by native session id. It checks
// active sessions first, then archived sessions so a non-resumable prior
// conversation can still contribute a provenance-labeled handoff reference.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID, err := validateCodexNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	for _, dir := range []string{"sessions", "archived_sessions"} {
		path, ok, err := findRollout(ctx, filepath.Join(configDir, dir), sessionID, false)
		if err != nil {
			return "", false, err
		}
		if ok {
			return path, true, nil
		}
	}
	return "", false, nil
}

func validateCodexNativeSessionID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, `/\\`+"\x00") {
		return "", fmt.Errorf("codex: invalid native session id")
	}
	if id, ok := canonicalCodexThreadID(value); ok {
		return id, nil
	}
	return value, nil
}
