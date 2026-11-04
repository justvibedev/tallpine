package deepseekharness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/hookutil"
)

// telemetryModeEnv selects dsh's OpenTelemetry session export. Its shipped
// default (FEEDBACK_ONLY) sends session records to DeepSeek after explicit
// feedback; DISABLED constructs no transport.
const telemetryModeEnv = "DSH_TELEMETRY_MODE"

// privacyPatch is a dsh profile overlay applied after the profile's own layers.
// dsh mounts @deepseek-ai/dsh-session-log-deepseek in every shipped profile and
// it uploads the canonical session log (including prompts and tool output) with
// each official-API request unless `enabled` is false. Overlaying the config
// keeps the plugin mounted, so plugins that inject it keep resolving.
const privacyPatch = `# Written by Tallpine. Disables dsh's session-log upload to DeepSeek.
- id: session-log-deepseek
  config:
    enabled: false
`

// PrivacyArgs returns the dsh launch flags that keep session logs local. The
// overlay lives under dataDir (Tallpine's data dir) and is rewritten only when its
// content changed. An empty dataDir is an error: falling back to a shared temp
// dir would write outside Tallpine's state root and trust a predictable path.
func PrivacyArgs(dataDir string) ([]string, error) {
	path, err := writeOverlay(dataDir, "privacy.patch.yml", privacyPatch)
	if err != nil {
		return nil, err
	}
	return []string{"--patch", path}, nil
}

// credentialsPatch re-enables dsh's credentials plugin. The headless profile
// composes it disabled (`dsh --profile headless --dump-config`), so a terminal
// launch fails with MISSING_CREDENTIAL even when a key was saved through dsh's
// web Models page; the acp and web profiles keep it enabled. Only the headless
// (terminal) launch adds this overlay.
//
// #nosec G101 -- a plugin id and a flag, no credential value
const credentialsPatch = `# Written by Tallpine. The headless profile ships with credentials off.
- id: credentials
  disabled: false
`

// HeadlessArgs is PrivacyArgs plus the credentials overlay, for the one-shot
// headless launch.
func HeadlessArgs(dataDir string) ([]string, error) {
	args, err := PrivacyArgs(dataDir)
	if err != nil {
		return nil, err
	}
	path, err := writeOverlay(dataDir, "credentials.patch.yml", credentialsPatch)
	if err != nil {
		return nil, err
	}
	hooks, err := hooksArgs(dataDir)
	if err != nil {
		return nil, err
	}
	return append(append(args, "--patch", path), hooks...), nil
}

// writeOverlay keeps one overlay file under dataDir/dsh, written atomically and
// only when its content changed. An empty dataDir is an error: falling back to a
// shared temp dir would write outside Tallpine's state root and trust a predictable path.
func writeOverlay(dataDir, name, content string) (string, error) {
	if dataDir == "" {
		return "", errors.New("deepseek-harness: no Tallpine data dir to hold the dsh overlays")
	}
	dir := filepath.Join(dataDir, "dsh")
	path := filepath.Join(dir, name)
	if current, err := os.ReadFile(path); err != nil || string(current) != content { //nolint:gosec // Tallpine-owned path
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("deepseek-harness: create overlay dir: %w", err)
		}
		if err := hookutil.AtomicWriteFile(path, []byte(content), 0o600); err != nil {
			return "", fmt.Errorf("deepseek-harness: write %s: %w", name, err)
		}
	}
	return path, nil
}

// ACPArgs is the argv tail that boots dsh's ACP server with the privacy overlay.
// Chat and model discovery share it so neither can launch without the overlay.
func ACPArgs(dataDir string) ([]string, error) {
	privacy, err := PrivacyArgs(dataDir)
	if err != nil {
		return nil, err
	}
	return append([]string{"--profile", "acp"}, privacy...), nil
}

// AugmentRuntimeEnv turns dsh's own telemetry export off unless the project
// environment already chose a mode. The session manager (terminal) and the
// native ACP binding (chat) both call it; model discovery uses it directly.
func (p *Plugin) AugmentRuntimeEnv(env map[string]string, _ string) {
	if env[telemetryModeEnv] == "" {
		env[telemetryModeEnv] = "DISABLED"
	}
}
