package activitydispatch

import (
	"encoding/json"
	"os"
	"testing"
)

// TestFactsMatchGolden pins Facts for every harness against golden output
// captured from the previous cli/hooks.go implementation (characterization).
func TestFactsMatchGolden(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var cases []struct{ Name, Agent, Event, Payload string }
	var golden []json.RawMessage
	read := func(path string, into any) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, into); err != nil {
			t.Fatal(err)
		}
	}
	read("testdata/facts/cases.json", &cases)
	read("testdata/facts/golden.json", &golden)
	if len(cases) != len(golden) {
		t.Fatalf("cases=%d golden=%d", len(cases), len(golden))
	}
	for i, cs := range cases {
		t.Run(cs.Name, func(t *testing.T) {
			facts := Facts(cs.Agent, cs.Event, []byte(cs.Payload))
			var want HookFacts
			if err := json.Unmarshal(golden[i], &want); err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(facts)
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("facts drifted\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}
