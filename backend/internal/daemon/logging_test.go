package daemon

import (
	"log"
	"log/slog"
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

func TestInstalledDefaultLoggerReachesDaemonLogOnceAndIsRestored(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TALLPINE_LOG_LEVEL", "")
	prevDefault, prevOut, prevFlags := slog.Default(), log.Writer(), log.Flags()

	l, closeLog := newLogger(dir)
	restore := installDefaultLogger(l)
	slog.Default().Warn("restart resume: worker not resumed", "session", "s1")
	log.Print("stdlib log line")
	restore()
	closeLog()

	if slog.Default() != prevDefault || log.Writer() != prevOut || log.Flags() != prevFlags {
		t.Fatal("installDefaultLogger's restore left a global logger behind")
	}
	data, err := os.ReadFile(daemonLogPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"restart resume: worker not resumed", "stdlib log line"} {
		if n := strings.Count(got, want); n != 1 {
			t.Fatalf("%q appears %d times in daemon.log, want 1:\n%s", want, n, got)
		}
	}
}
