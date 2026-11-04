package statefence

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// A restored fenced session keeps zsh's temp files in its own temp dir (B521).
func TestRestoreEnvPointsTempPrefixAtSessionTempDir(t *testing.T) {
	env := RestoreEnv("/run.json", t.TempDir(), ports.SessionRef{ID: "s-1"})
	tmp := env["TMPDIR"]
	if tmp == "" || env[sessionscratch.TempPrefixEnv] != sessionscratch.TempPrefix(tmp) {
		t.Fatalf("TMPPREFIX = %q, want inside TMPDIR %q", env[sessionscratch.TempPrefixEnv], tmp)
	}
}
