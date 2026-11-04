package daemon

import (
	"os"
	"strings"
	"testing"
)

func TestNewLoggerWritesInfoToRotatingFileUnderDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TALLPINE_LOG_LEVEL", "")
	log, closeLog := newLogger(dir)
	log.Debug("noisy debug line")
	log.Info("useful info line", "k", "v")
	closeLog()

	data, err := os.ReadFile(daemonLogPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "useful info line") {
		t.Fatalf("info line missing from daemon.log:\n%s", got)
	}
	if strings.Contains(got, "noisy debug line") {
		t.Fatalf("debug line written at the default level:\n%s", got)
	}
}

func TestNewLoggerHonoursLogLevelEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TALLPINE_LOG_LEVEL", "debug")
	log, closeLog := newLogger(dir)
	log.Debug("debug on request")
	closeLog()
	data, _ := os.ReadFile(daemonLogPath(dir))
	if !strings.Contains(string(data), "debug on request") {
		t.Fatalf("TALLPINE_LOG_LEVEL=debug ignored:\n%s", data)
	}
}
