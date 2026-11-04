package agentpolicy

import (
	"context"
	"os"
	"runtime"
	"time"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// loadManagedFile returns the file as JSON. On macOS the managed file is a
// (possibly binary) plist written by MDM, converted with the system plutil;
// everywhere else, and for a plain-JSON file on macOS, it is read directly.
func loadManagedFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "darwin" || looksLikeJSON(data) {
		return data, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tallpineprocess.CommandContext(ctx, "/usr/bin/plutil", "-convert", "json", "-o", "-", path).Output() //nolint:gosec // fixed system tool, path from managedPath
}

func looksLikeJSON(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		}
		return b == '{'
	}
	return false
}
