package testenv

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStaleDaemonsFiltersOrphansInE2EDirs(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"tallpine-e2e-bin123", "tallpine-e2e-bin-456"} {
		mkOwner(t, filepath.Join(tmp, d), "999999999") // dead owner
	}
	mkOwner(t, filepath.Join(tmp, "tallpine-e2e-binlive"), strconv.Itoa(os.Getpid())) // live owner
	ps := "" +
		"101 1 TMP/tallpine-e2e-bin123/tallpine daemon\n" + // orphan: reap
		"102 1 TMP/tallpine-e2e-bin-456/tallpine daemon\n" + // orphan: reap
		"103 555 TMP/tallpine-e2e-bin123/tallpine daemon\n" + // live parent: keep
		"104 1 /Applications/Vibe/tallpine daemon\n" + // real app: keep
		"107 1 TMP/tallpine-e2e-binlive/tallpine chat-host\n" + // orphan but owner alive: keep
		"105 1 /tmp/other/tallpine daemon\n" + // wrong dir: keep
		"106 1 /home/x/tallpine-e2e-bin1/tallpine daemon\n" // outside tmp: keep
	ps = strings.ReplaceAll(ps, "TMP", tmp)
	got := staleDaemons(ps, []string{tmp})
	if len(got) != 2 || got[0].pid != 101 || got[1].pid != 102 {
		t.Fatalf("staleDaemons = %+v, want pids 101 and 102 only", got)
	}
}

func mkOwner(t *testing.T, dir, pid string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte(pid), 0o600); err != nil {
		t.Fatal(err)
	}
}
