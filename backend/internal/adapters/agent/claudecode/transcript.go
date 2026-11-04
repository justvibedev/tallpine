package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// findTranscript is the one Claude transcript lookup used by Exists, Probe and
// Locate. Claude stores <config>/projects/<project-dir>/<session-id>.jsonl; a
// zero-byte file is a reserved id Claude has not materialised, so it is not a
// transcript (Claude rejects --resume for it). sessionID must be canonical.
func findTranscript(ctx context.Context, configDir, sessionID string) (string, bool, error) {
	projectsDir := filepath.Join(configDir, "projects")
	projects, err := os.ReadDir(projectsDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claude-code: read transcript projects: %w", err)
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(projectsDir, project.Name(), sessionID+".jsonl")
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Mode().IsRegular() && info.Size() > 0:
			return path, true, nil
		case err == nil, errors.Is(err, os.ErrNotExist):
		default:
			return "", false, fmt.Errorf("claude-code: stat transcript: %w", err)
		}
	}
	return "", false, nil
}
