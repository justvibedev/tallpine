package previewserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
)

type launchFile struct {
	Version        int             `json:"version"`
	Configurations []Configuration `json:"configurations"`
}

// Configuration is one named server entry in .tallpine/launch.json. ${PORT} is
// expanded in runtimeArgs, url, and env values after Tallpine chooses the port.
type Configuration struct {
	Name               string            `json:"name"`
	RuntimeExecutable  string            `json:"runtimeExecutable"`
	RuntimeArgs        []string          `json:"runtimeArgs,omitempty"`
	Cwd                string            `json:"cwd,omitempty"`
	Port               int               `json:"port,omitempty"`
	AutoPort           bool              `json:"autoPort,omitempty"`
	URL                string            `json:"url,omitempty"`
	Env                map[string]string `json:"env,omitempty"`
	TargetKind         TargetKind        `json:"targetKind,omitempty"`
	ReadyTimeoutMillis int               `json:"readyTimeoutMs,omitempty"`
}

func loadConfiguration(workspacePath, requestedName string) (Configuration, error) {
	if strings.TrimSpace(workspacePath) == "" {
		return Configuration{}, serviceError("PREVIEW_WORKSPACE_MISSING", "session workspace is unavailable")
	}
	configPath := filepath.Join(workspacePath, filepath.FromSlash(ConfigPath))
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		if legacy, legacyErr := os.ReadFile(filepath.Join(workspacePath, filepath.FromSlash(LegacyConfigPath))); legacyErr == nil {
			data, err = legacy, nil
		}
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Configuration{}, serviceError(
				"PREVIEW_CONFIG_NOT_FOUND",
				fmt.Sprintf("preview configuration not found at %s", ConfigPath),
			)
		}
		return Configuration{}, serviceError("PREVIEW_CONFIG_INVALID", fmt.Sprintf("read %s: %v", ConfigPath, err))
	}
	var file launchFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return Configuration{}, serviceError("PREVIEW_CONFIG_INVALID", fmt.Sprintf("decode %s: %v", ConfigPath, err))
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return Configuration{}, serviceError("PREVIEW_CONFIG_INVALID", fmt.Sprintf("decode %s: %v", ConfigPath, err))
	}
	if file.Version != 1 {
		return Configuration{}, serviceError("PREVIEW_CONFIG_INVALID", "preview configuration version must be 1")
	}
	if len(file.Configurations) == 0 {
		return Configuration{}, serviceError("PREVIEW_CONFIG_INVALID", "preview configuration has no configurations")
	}
	requestedName = strings.TrimSpace(requestedName)
	if requestedName == "" && len(file.Configurations) > 1 {
		return Configuration{}, serviceError(
			"PREVIEW_CONFIGURATION_REQUIRED",
			"multiple preview configurations exist; specify one by name",
		)
	}
	seen := make(map[string]struct{}, len(file.Configurations))
	var selected *Configuration
	for i := range file.Configurations {
		cfg := file.Configurations[i]
		cfg.Name = strings.TrimSpace(cfg.Name)
		if cfg.Name == "" {
			return Configuration{}, serviceError("PREVIEW_CONFIG_INVALID", "preview configuration name is required")
		}
		if _, duplicate := seen[cfg.Name]; duplicate {
			return Configuration{}, serviceError(
				"PREVIEW_CONFIG_INVALID",
				fmt.Sprintf("duplicate preview configuration name %q", cfg.Name),
			)
		}
		seen[cfg.Name] = struct{}{}
		if err := validateConfiguration(&cfg); err != nil {
			return Configuration{}, err
		}
		if requestedName == "" || cfg.Name == requestedName {
			candidate := cfg
			selected = &candidate
		}
	}
	if selected != nil {
		return *selected, nil
	}
	return Configuration{}, serviceError(
		"PREVIEW_CONFIGURATION_NOT_FOUND",
		fmt.Sprintf("preview configuration %q was not found", requestedName),
	)
}

func validateConfiguration(cfg *Configuration) error {
	cfg.RuntimeExecutable = strings.TrimSpace(cfg.RuntimeExecutable)
	if cfg.RuntimeExecutable == "" {
		return serviceError(
			"PREVIEW_CONFIG_INVALID",
			fmt.Sprintf("preview configuration %q requires runtimeExecutable", cfg.Name),
		)
	}
	if cfg.Port < 0 || cfg.Port > 65535 || (!cfg.AutoPort && cfg.Port == 0) {
		return serviceError(
			"PREVIEW_CONFIG_INVALID",
			fmt.Sprintf("preview configuration %q has an invalid port", cfg.Name),
		)
	}
	if cfg.TargetKind == "" {
		cfg.TargetKind = TargetApp
	}
	if cfg.TargetKind != TargetApp && cfg.TargetKind != TargetAPI {
		return serviceError(
			"PREVIEW_CONFIG_INVALID",
			fmt.Sprintf("preview configuration %q targetKind must be app or api", cfg.Name),
		)
	}
	if cfg.ReadyTimeoutMillis < 0 || time.Duration(cfg.ReadyTimeoutMillis)*time.Millisecond > maxReadyTimeout {
		return serviceError(
			"PREVIEW_CONFIG_INVALID",
			fmt.Sprintf("preview configuration %q readyTimeoutMs must be between 0 and %d", cfg.Name, maxReadyTimeout.Milliseconds()),
		)
	}
	for key := range cfg.Env {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key, "=\x00") {
			return serviceError(
				"PREVIEW_CONFIG_INVALID",
				fmt.Sprintf("preview configuration %q has an invalid env key %q", cfg.Name, key),
			)
		}
	}
	return nil
}

func resolveWorkingDirectory(workspacePath, relative string) (string, error) {
	workspaceAbs, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", serviceError("PREVIEW_CONFIG_INVALID", "resolve session workspace: "+err.Error())
	}
	relative = strings.TrimSpace(relative)
	if filepath.IsAbs(relative) {
		return "", serviceError("PREVIEW_CONFIG_INVALID", "preview cwd must be relative to the session workspace")
	}
	target := filepath.Clean(filepath.Join(workspaceAbs, relative))
	rel, err := filepath.Rel(workspaceAbs, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", serviceError("PREVIEW_CONFIG_INVALID", "preview cwd escapes the session workspace")
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return "", serviceError("PREVIEW_CONFIG_INVALID", fmt.Sprintf("preview cwd %q is not a directory", relative))
	}
	// Reject symlinked descendants instead of resolving them. This prevents a
	// configured cwd from escaping the worker workspace while avoiding
	// platform-specific canonicalization of trusted workspace ancestors.
	current := workspaceAbs
	if rel != "." {
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			entry, err := os.Lstat(current)
			if err != nil {
				return "", serviceError("PREVIEW_CONFIG_INVALID", fmt.Sprintf("inspect preview cwd %q: %v", relative, err))
			}
			if entry.Mode()&os.ModeSymlink != 0 {
				return "", serviceError("PREVIEW_CONFIG_INVALID", "preview cwd cannot contain symlinks")
			}
		}
	}
	return target, nil
}

func resolveTargetURL(raw string, port int) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "http://127.0.0.1:${PORT}/"
	}
	parsed, err := url.Parse(interpolatePort(raw, port))
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return "", serviceError("PREVIEW_CONFIG_INVALID", "preview url must be an http loopback URL; TLS previews are not supported")
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return "", serviceError("PREVIEW_CONFIG_INVALID", "preview url must use localhost or a loopback address")
	}
	return parsed.String(), nil
}

func reservePort(preferred int, auto bool) (int, net.Listener, error) {
	if !auto {
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(preferred)))
		if err != nil {
			return 0, nil, serviceError(
				"PREVIEW_PORT_IN_USE",
				fmt.Sprintf("configured preview port %d is already in use", preferred),
			)
		}
		return preferred, listener, nil
	}
	address := "127.0.0.1:0"
	if preferred > 0 {
		address = net.JoinHostPort("127.0.0.1", strconv.Itoa(preferred))
	}
	listener, err := net.Listen("tcp", address)
	if err != nil && preferred > 0 {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return 0, nil, fmt.Errorf("select preview port: %w", err)
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		_ = listener.Close()
		return 0, nil, errors.New("selected preview listener did not return a TCP address")
	}
	return tcpAddress.Port, listener, nil
}

func previewEnvironment(
	base []string,
	projectEnv map[string]string,
	configured map[string]string,
	sessionID domain.SessionID,
	port int,
) []string {
	allowed := map[string]struct{}{
		"PATH": {}, "HOME": {}, "USERPROFILE": {}, "SYSTEMROOT": {}, "COMSPEC": {},
		"PATHEXT": {}, "TEMP": {}, "TMP": {}, "TMPDIR": {}, "SHELL": {}, "LANG": {},
		"LC_ALL": {}, "APPDATA": {}, "LOCALAPPDATA": {},
	}
	env := make([]string, 0, len(allowed)+len(projectEnv)+len(configured)+3)
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if _, keep := allowed[strings.ToUpper(key)]; ok && keep {
			env = append(env, item)
		}
	}
	interpolated := make(map[string]string, len(configured))
	for key, value := range configured {
		interpolated[key] = interpolatePort(value, port)
	}
	for key, value := range agentlaunch.MergeEnv(projectEnv, interpolated) {
		env = append(env, key+"="+value)
	}
	env = append(env,
		"PORT="+strconv.Itoa(port),
		"TALLPINE_PREVIEW_PORT="+strconv.Itoa(port),
		"TALLPINE_SESSION_ID="+string(sessionID),
	)
	return env
}

func interpolatePort(value string, port int) string {
	return strings.ReplaceAll(value, "${PORT}", strconv.Itoa(port))
}

func readyTimeout(milliseconds int) time.Duration {
	if milliseconds <= 0 {
		return defaultReadyTimeout
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(strings.TrimSpace(host), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
