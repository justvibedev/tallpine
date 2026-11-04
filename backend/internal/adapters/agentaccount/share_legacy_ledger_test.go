package agentaccount

import (
	"os"
	"path/filepath"
	"testing"
)

// The per-account record written before the rename (.ao-shared.json) is still
// honoured, so keys the user changed on an account are not treated as fresh.
func TestReadLedgerFallsBackToPreRenameRecord(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".ao-shared.json"), []byte(`{"settings.json":{"theme":"dark"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l := readLedger(home)
	if l.fresh || string(l.files["settings.json"]["theme"]) != `"dark"` {
		t.Fatalf("legacy ledger ignored: fresh=%v files=%v", l.fresh, l.files)
	}
	if fresh := readLedger(t.TempDir()); !fresh.fresh {
		t.Fatal("a home with no record must start fresh")
	}
}
