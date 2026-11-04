package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/procmem"
)

type fixedHibernationSettings domain.ChatHibernationPolicy

func (f fixedHibernationSettings) ChatHibernation(context.Context) domain.ChatHibernationPolicy {
	return domain.ChatHibernationPolicy(f)
}

type hibernationProjects map[string]domain.ProjectRecord

func (p hibernationProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	if id == "broken" {
		return domain.ProjectRecord{}, false, errors.New("db down")
	}
	rec, ok := p[id]
	return rec, ok, nil
}

func TestChatHibernationPolicyByKindAndProject(t *testing.T) {
	projects := hibernationProjects{
		"on":  {ID: "on"},
		"off": {ID: "off", Config: domain.ProjectConfig{ChatHibernation: &domain.ChatHibernationConfig{Disabled: true}}},
	}
	policy := chatHibernationPolicy(fixedHibernationSettings(domain.DefaultChatHibernationPolicy()), projects)
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		rec     domain.SessionRecord
		idle    time.Duration
		allowed bool
	}{
		{"worker", domain.SessionRecord{ProjectID: "on", Kind: domain.KindWorker}, 20 * time.Minute, true},
		{"orchestrator", domain.SessionRecord{ProjectID: "on", Kind: domain.KindOrchestrator}, time.Hour, true},
		{"project opt-out", domain.SessionRecord{ProjectID: "off", Kind: domain.KindWorker}, 0, false},
		{"unknown project", domain.SessionRecord{ProjectID: "gone", Kind: domain.KindWorker}, 0, false},
		{"unreadable project", domain.SessionRecord{ProjectID: "broken", Kind: domain.KindWorker}, 0, false},
	} {
		idle, allowed := policy(ctx, tc.rec)
		if idle != tc.idle || allowed != tc.allowed {
			t.Errorf("%s: policy = %s,%v want %s,%v", tc.name, idle, allowed, tc.idle, tc.allowed)
		}
	}
	off := domain.DefaultChatHibernationPolicy()
	off.Enabled = false
	if _, allowed := chatHibernationPolicy(fixedHibernationSettings(off), projects)(ctx, domain.SessionRecord{ProjectID: "on"}); allowed {
		t.Fatal("globally disabled hibernation allowed a session to sleep")
	}
}

func writeTestHostDescriptor(t *testing.T, dataDir, sessionID string, pid int) {
	t.Helper()
	dir := filepath.Join(dataDir, "chat-hosts", sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"version": 1, "sessionId": sessionID, "address": "127.0.0.1:1", "token": "t", "pid": pid,
	})
	if err := os.WriteFile(filepath.Join(dir, "host.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestChatBackgroundProcessesVetoesDetachedWork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups are a unix concept")
	}
	dataDir := t.TempDir()
	host := os.Getpid() // HostPID requires a live pid.
	writeTestHostDescriptor(t, dataDir, "mer-1", host)
	table := func(rows string) func(context.Context) (*procmem.GroupTable, error) {
		return func(context.Context) (*procmem.GroupTable, error) { return procmem.ParseGroups(rows) }
	}
	idle := fmt.Sprintf("%[1]d 1 %[1]d S\n900001 %[1]d 900001 S\n900002 900001 900001 S\n900003 900002 900001 S\n", host)
	busy := idle + "900004 900002 900004 S\n900005 900004 900004 S\n"
	ctx := context.Background()

	if got, err := chatBackgroundProcesses(dataDir, table(idle), nil)(ctx, "mer-1"); err != nil || got {
		t.Fatalf("idle agent = %v, %v; want not busy", got, err)
	}
	if got, err := chatBackgroundProcesses(dataDir, table(busy), nil)(ctx, "mer-1"); err != nil || !got {
		t.Fatalf("agent with a background shell = %v, %v; want busy", got, err)
	}
	if _, err := chatBackgroundProcesses(dataDir, table(idle), nil)(ctx, "mer-2"); !errors.Is(err, errChatHostNotRunning) {
		t.Fatalf("missing host err = %v", err)
	}
	if _, err := chatBackgroundProcesses(dataDir, table("1 0 1 S\n"), nil)(ctx, "mer-1"); !errors.Is(err, errChatHostNotRunning) {
		t.Fatalf("host absent from table err = %v", err)
	}
	failing := func(context.Context) (*procmem.GroupTable, error) { return nil, errors.New("ps failed") }
	if _, err := chatBackgroundProcesses(dataDir, failing, nil)(ctx, "mer-1"); err == nil {
		t.Fatal("unreadable process table reported no error")
	}
}
