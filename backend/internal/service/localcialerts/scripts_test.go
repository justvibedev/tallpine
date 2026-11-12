package localcialerts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoScript is a script under the repo's scripts/ directory.
func repoScript(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	p := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "scripts", rel)
	if _, err := os.Stat(p); err != nil {
		t.Skipf("%s not found: %v", rel, err)
	}
	return p
}

// The production writer and the script reader agree with this reader: an
// alert tp_record_alert appends is delivered, and scripts/local-ci/alerts then
// finds nothing new because the cursor is shared.
func TestReaderMatchesTheScripts(t *testing.T) {
	for _, bin := range []string{"bash", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	f := newFixture(t)
	alerts := filepath.Join(f.dir, "results", "alerts.tsv")
	record := exec.Command("bash", "-c", `. "$1" && tp_record_alert "$2" "$3"`, "bash",
		repoScript(t, "lib/notify.sh"), alerts, "[local-ci] build abc OK: app.\tLog: x")
	record.Env = append(os.Environ(), "TALLPINE_ALERT_DESKTOP_NOTIFY=")
	if out, err := record.CombinedOutput(); err != nil {
		t.Fatalf("tp_record_alert: %v\n%s", err, out)
	}
	if err := f.reader.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || !strings.HasSuffix(f.sent[0].msg, " [local-ci] build abc OK: app. Log: x") {
		t.Fatalf("recorded alert not delivered: %+v", f.sent)
	}
	peek := exec.Command(repoScript(t, "local-ci/alerts"), "--peek")
	peek.Env = append(os.Environ(), "TALLPINE_LOCAL_CI_DIR="+f.dir, "OUT=")
	out, err := peek.CombinedOutput()
	if err != nil {
		t.Fatalf("alerts --peek: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the script would pass the alert on again:\n%s", out)
	}
}
