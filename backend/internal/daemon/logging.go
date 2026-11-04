package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/logrotate"
)

const (
	// daemonLogMaxBytes caps one daemon.log file; with daemonLogBackups rotated
	// copies the log never exceeds ~(backups+1)*max on disk.
	daemonLogMaxBytes = 20 << 20
	daemonLogBackups  = 3
)

// daemonLogPath is where the daemon writes its own log, independent of how it
// was launched (a Finder-launched app has no terminal to read stderr from).
func daemonLogPath(dataDir string) string {
	return filepath.Join(dataDir, "logs", "daemon.log")
}

// logLevelFromEnv maps TALLPINE_LOG_LEVEL (debug, info, warn, error) to a slog level.
// The default is Info: Debug floods the log (and, when read through a pipe, the
// supervisor) with per-request and per-poll lines.
func logLevelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(brand.Env("LOG_LEVEL")))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// newLogger returns the daemon's slog logger and a close func for its log file.
// It writes to a size-capped rotating file under the data dir and, best effort,
// to stderr (the desktop supervisor still reads the "listening" line and the
// startup tail from the pipe, but no longer echoes it to its console). A file
// that cannot be opened degrades to stderr only, with a warning.
func newLogger(dataDir string) (*slog.Logger, func()) {
	opts := &slog.HandlerOptions{Level: logLevelFromEnv()}
	file, err := logrotate.Open(daemonLogPath(dataDir), daemonLogMaxBytes, daemonLogBackups)
	if err != nil {
		log := slog.New(slog.NewTextHandler(os.Stderr, opts))
		log.Warn("daemon log file unavailable; logging to stderr only", "path", daemonLogPath(dataDir), "err", err)
		return log, func() {}
	}
	return slog.New(slog.NewTextHandler(teeWriter{file: file, stderr: os.Stderr}, opts)), func() { _ = file.Close() }
}

// teeWriter writes every record to the log file and then to stderr. A dead
// stderr (the supervisor's pipe closed after the app quit) must never cost a
// log line or fail the write, so its error is ignored.
type teeWriter struct {
	file   io.Writer
	stderr io.Writer
}

func (t teeWriter) Write(p []byte) (int, error) {
	n, err := t.file.Write(p)
	_, _ = t.stderr.Write(p)
	return n, err
}
