package modelcatalog

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/pkg/agentcreds"
)

// BinaryVersion returns a short non-sensitive executable-metadata fingerprint
// for cache invalidation. It deliberately does not start the agent: statting
// the resolved executable keeps cache validation fast and side-effect free.
func BinaryVersion(ctx context.Context, binary string) string {
	if ctx.Err() != nil || strings.TrimSpace(binary) == "" {
		return ""
	}
	resolved, err := exec.LookPath(binary)
	if err != nil {
		return ""
	}
	if evaluated, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolved = evaluated
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(resolved))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatInt(info.Size(), 10)))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(strconv.FormatInt(info.ModTime().UnixNano(), 10)))
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}

// CatalogFingerprint hashes every stable discovery input for an agent: the
// resolved executable plus the configuration and credentials its discovery
// reads. Only the digest is returned or persisted.
func CatalogFingerprint(ctx context.Context, agentID, binary, workingDir string, env map[string]string) string {
	binaryVersion := BinaryVersion(ctx, binary)
	config := discoveryConfigInputs(ctx, agentID, workingDir, env)
	// Refresh pre-provider catalogs after upgrading and when observed credential
	// presence changes. The fingerprint includes names only, never key material.
	if agentID == "kiro" {
		// Kept byte-identical to the fingerprint earlier daemons wrote for Kiro so
		// upgrading does not invalidate its cached catalog.
		config += ";providers-v2="
	}
	if config == "" {
		// Keep the executable-only fingerprint byte-identical to what earlier
		// daemons wrote, so upgrading does not invalidate every cached catalog.
		return binaryVersion
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(binaryVersion))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(config))
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}

// discoveryConfigInputs returns the configuration an agent's discovery consults,
// or "" when the catalog depends on the binary alone.
func discoveryConfigInputs(ctx context.Context, agentID, workingDir string, env map[string]string) string {
	if agentID == "claude-code" {
		return "config=" + claudeCodeDiscoveryFingerprint(ctx, workingDir, env)
	}
	if agentID == "deepseek-harness" {
		return "config=" + deepseekProfileFingerprint(env)
	}
	return ""
}

// deepseekProfileFingerprint hashes the files of dsh's ACP profile. The profile
// decides the model route, so a route changed in dsh's setup flow must not leave
// the day's cached choices in place.
func deepseekProfileFingerprint(env map[string]string) string {
	home := strings.TrimSpace(env["DSH_HOME"])
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(user, ".dsh")
	}
	hash := sha256.New()
	for _, name := range [...]string{"cordis.yml", "cordis.patch.yml", "package.json"} {
		raw, _ := readModelConfig(filepath.Join(home, "profiles", "acp", name))
		_, _ = hash.Write(append([]byte(name+"\x00"), raw...))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}

func claudeCodeDiscoveryFingerprint(ctx context.Context, workingDir string, env map[string]string) string {
	settings := agentcreds.ResolveClaudeSettings(ctx, workingDir, env, agentcreds.ResolveOptions{})
	hash := sha256.New()
	_, _ = hash.Write([]byte("model\x00" + settings.Model + "\x00"))
	keys := make([]string, 0, len(settings.Env))
	for key := range settings.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, _ = hash.Write([]byte(key + "\x00" + settings.Env[key] + "\x00"))
	}
	if raw, err := readModelConfig(settings.Env["GOOGLE_APPLICATION_CREDENTIALS"]); err == nil {
		_, _ = hash.Write(raw)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)[:8])
}
