package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInAgentSession(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    map[string]string
		wantID string
		wantOK bool
	}{
		{name: "outside a session", env: map[string]string{}},
		{name: "tallpine session", env: map[string]string{"TALLPINE_SESSION_ID": "aox-7"}, wantID: "aox-7", wantOK: true},
		{name: "official tallpine session", env: map[string]string{"AO_SESSION_ID": "tallpine-77"}, wantID: "tallpine-77", wantOK: true},
		{name: "explicit opt-in", env: map[string]string{"TALLPINE_SESSION_ID": "aox-7", "TALLPINE_DEV_REAL_DATA": "1"}},
		{name: "blank id", env: map[string]string{"TALLPINE_SESSION_ID": "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := InAgentSession(func(name string) string { return tc.env[name] })
			if id != tc.wantID || ok != tc.wantOK {
				t.Fatalf("InAgentSession = (%q, %v), want (%q, %v)", id, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

func TestCheckDaemonIsolation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realRoot := filepath.Join(home, ".tallpine")
	for _, tc := range []struct {
		name     string
		dataDir  string
		runFile  string
		wantFail string
	}{
		{name: "inherited production state", dataDir: filepath.Join(realRoot, "data"), runFile: filepath.Join(realRoot, "running.json"), wantFail: "TALLPINE_DATA_DIR"},
		{name: "production run file only", dataDir: filepath.Join(home, "scratch", "data"), runFile: filepath.Join(realRoot, "running.json"), wantFail: "TALLPINE_RUN_FILE"},
		{name: "state root itself", dataDir: realRoot, runFile: filepath.Join(home, "scratch", "running.json"), wantFail: "TALLPINE_DATA_DIR"},
		{name: "dev state", dataDir: filepath.Join(realRoot, "dev", "lab", "data"), runFile: filepath.Join(realRoot, "dev", "lab", "running.json")},
		{name: "scratch outside ~/.tallpine", dataDir: "/tmp/vo-lab/data", runFile: "/tmp/vo-lab/running.json"},
		{name: "sibling with the same prefix", dataDir: filepath.Join(home, ".vo-lab", "data"), runFile: filepath.Join(home, ".vo-lab", "running.json")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckDaemonIsolation(Config{DataDir: tc.dataDir, RunFilePath: tc.runFile}, "aox-7")
			switch {
			case tc.wantFail == "" && err != nil:
				t.Fatalf("CheckDaemonIsolation: %v, want nil", err)
			case tc.wantFail != "" && (err == nil || !strings.Contains(err.Error(), tc.wantFail) || !strings.Contains(err.Error(), "TALLPINE_DEV_REAL_DATA=1")):
				t.Fatalf("CheckDaemonIsolation = %v, want a refusal naming %s and the opt-in", err, tc.wantFail)
			}
		})
	}
}

func TestDataDirHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("TALLPINE_DATA_DIR", "")
	if got := DefaultDataDir(); got != filepath.Join(home, ".tallpine", "data") {
		t.Fatalf("DefaultDataDir = %q", got)
	}
	if _, ok := DataDirFromEnv(); ok {
		t.Fatal("blank override must count as unset")
	}
	t.Setenv("TALLPINE_DATA_DIR", "  ")
	if _, ok := DataDirFromEnv(); ok {
		t.Fatal("whitespace override must count as unset")
	}
	abs := filepath.Join(home, "custom")
	t.Setenv("TALLPINE_DATA_DIR", abs)
	if got, ok := DataDirFromEnv(); !ok || got != abs {
		t.Fatalf("DataDirFromEnv = %q,%v", got, ok)
	}
	t.Setenv("TALLPINE_DATA_DIR", "rel")
	if got, ok := DataDirFromEnv(); !ok || !filepath.IsAbs(got) {
		t.Fatalf("relative override must be absolutized, got %q,%v", got, ok)
	}
}

func TestResolveDataDirUsesTheSameHelpers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"", "   "} {
		t.Setenv("TALLPINE_DATA_DIR", v)
		got, err := resolveDataDir()
		if err != nil || got != DefaultDataDir() {
			t.Fatalf("override %q: resolveDataDir = %q,%v want default %q", v, got, err, DefaultDataDir())
		}
	}
	t.Setenv("TALLPINE_DATA_DIR", filepath.Join(home, "x"))
	want, _ := DataDirFromEnv()
	if got, err := resolveDataDir(); err != nil || got != want {
		t.Fatalf("explicit: %q,%v want %q", got, err, want)
	}
}
