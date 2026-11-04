package deepseekharness

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/authprobe"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// deepseekCredentialKey names the credential dsh stores for the first-party
// "deepseek-official" route. Other entries in the same store (an OpenCode Go key,
// say) authorize different providers and are not evidence dsh is ready.
const deepseekCredentialKey = "DEEPSEEK_API_KEY" // #nosec G101 -- a credential's name, not a value

// AuthStatus reports whether dsh has a DeepSeek credential. Its ACP server
// advertises no auth methods, so the only local evidence is the credential store
// and then the environment. Absent evidence stays unknown: dsh may be routed
// through a provider its profile configures, which Tallpine cannot see from here.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if path := credentialsPath(); path != "" && storeHasDeepSeekKey(path) {
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey))
		return ports.AgentAuthStatusAuthorized, nil
	}
	if authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, deepseekCredentialKey) != "" {
		return ports.AgentAuthStatusAuthorized, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

// credentialsPath resolves dsh's credential store: DSH_CREDENTIALS_PATH, else
// $DSH_HOME/.credentials.yaml, else ~/.dsh/.credentials.yaml.
func credentialsPath() string {
	if override := strings.TrimSpace(os.Getenv("DSH_CREDENTIALS_PATH")); override != "" {
		return override
	}
	home := strings.TrimSpace(os.Getenv("DSH_HOME"))
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil || user == "" {
			return ""
		}
		home = filepath.Join(user, ".dsh")
	}
	return filepath.Join(home, ".credentials.yaml")
}

// storeHasDeepSeekKey reads the store without exposing it. A current dsh writes
// `version: 1` with secrets under `refs:`; the pre-release flat layout is refused
// by dsh itself, so it is no evidence here. A ref holds the secret directly or
// as {payload: {secret: ...}}. Scoped `records:` carry no such ref and stay
// unknown rather than claim a route Tallpine cannot attribute to DeepSeek.
func storeHasDeepSeekKey(path string) bool {
	data, err := os.ReadFile(path) //nolint:gosec // the dsh credential store resolved above
	if err != nil {
		return false
	}
	var store struct {
		Version int            `yaml:"version"`
		Refs    map[string]any `yaml:"refs"`
	}
	if yaml.Unmarshal(data, &store) != nil || store.Version != 1 {
		return false
	}
	return secretPresent(store.Refs[deepseekCredentialKey])
}

func secretPresent(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case map[string]any:
		return secretPresent(v["secret"]) || secretPresent(v["payload"])
	}
	return false
}
