package persistenthost

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A host whose state dir is deleted can never be reconnected to: it must exit
// instead of outliving its test or data dir forever (B330).
func TestHostExitsWhenStateDirDeleted(t *testing.T) {
	old := stateDirCheckInterval
	stateDirCheckInterval = 20 * time.Millisecond
	t.Cleanup(func() { stateDirCheckInterval = old })

	dataDir := t.TempDir()
	cfg := Config{
		SessionID: "statedir-gone", DataDir: dataDir, Workdir: t.TempDir(),
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), cfg) }()
	awaitDescriptor(t, dataDir, cfg.SessionID)
	t.Cleanup(func() { _ = Shutdown(context.Background(), dataDir, cfg.SessionID) })

	if err := os.RemoveAll(filepath.Join(dataDir, "chat-hosts")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("host kept running after its state dir was deleted")
	}
}
