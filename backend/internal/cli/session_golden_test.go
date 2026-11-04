package cli

// session_golden_test.go pins the human and --json output of the session
// commands from the fixed driftSession fixture. The --json output of get, top,
// exit-agent and resume-agent is the daemon's apitypes shape (a superset of the
// old CLI-local subset) by design; any further change must be a reviewed
// golden update (UPDATE_GOLDEN=1).

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

var goldenAge = regexp.MustCompile(`\b\d+[smhd]\b( ago)?`)

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with UPDATE_GOLDEN=1)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s drifted.\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestSessionOutputGolden(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"ls.txt", []string{"session", "ls", "--project", "mer"}},
		{"ls.json", []string{"session", "ls", "--project", "mer", "--json"}},
		{"get.txt", []string{"session", "get", "mer-7"}},
		{"get.json", []string{"session", "get", "mer-7", "--json"}},
		{"exit-agent.json", []string{"session", "exit-agent", "mer-7", "--json"}},
		{"resume-agent.json", []string{"session", "resume-agent", "mer-7", "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runDriftSession(t, &driftSessionService{}, tc.args...)
			// Relative ages depend on the wall clock; mask them.
			checkGolden(t, tc.name, goldenAge.ReplaceAll(out, []byte("<age>")))
		})
	}
}

func TestSessionTopJSONGolden(t *testing.T) {
	rss, procs := uint64(2048), 3
	var out sessionTopOutput
	out.Data = []sessionTopEntry{{SessionView: apitypes.SessionView{Session: driftSession()}, RSSBytes: &rss, ProcessCount: &procs}}
	out.Meta.TotalRSSBytes = rss
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "top.json", append(raw, '\n'))
}
