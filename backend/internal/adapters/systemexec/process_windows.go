//go:build windows

package systemexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

func commandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	extension := filepath.Ext(resolved)
	if !strings.EqualFold(extension, ".cmd") && !strings.EqualFold(extension, ".bat") {
		return tallpineprocess.CommandContext(ctx, resolved, args...), nil //nolint:gosec // Callers supply server-owned argv.
	}

	shell := strings.TrimSpace(os.Getenv("ComSpec"))
	if shell == "" {
		shell = "cmd.exe"
	}
	cmd := tallpineprocess.CommandContext(ctx, shell) //nolint:gosec // ComSpec is Windows' configured batch interpreter.
	cmd.Args = nil
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = `/d /s /c "` + tallpineprocess.BatchCommandLine(resolved, args) + `"`
	return cmd, nil
}

func refreshExecutablePath() {
	paths := []string{os.Getenv("Path")}
	paths = append(paths,
		registryPath(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`),
		registryPath(registry.CURRENT_USER, `Environment`),
	)
	if merged := mergeWindowsPath(paths...); merged != "" {
		_ = os.Setenv("Path", merged)
	}
}

func registryPath(root registry.Key, keyPath string) string {
	key, err := registry.OpenKey(root, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = key.Close() }()
	value, valueType, err := key.GetStringValue("Path")
	if err != nil {
		return ""
	}
	if valueType == registry.EXPAND_SZ {
		if expanded, expandErr := registry.ExpandString(value); expandErr == nil {
			value = expanded
		}
	}
	return value
}

func mergeWindowsPath(values ...string) string {
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, value := range values {
		for _, entry := range strings.Split(value, ";") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			key := strings.ToLower(strings.TrimRight(entry, `\/`))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, entry)
		}
	}
	return strings.Join(merged, ";")
}
