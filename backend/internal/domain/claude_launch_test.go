package domain

import "testing"

func TestClaudeLaunchFor(t *testing.T) {
	c := ProjectConfig{
		ClaudePlugins:    map[string]map[string]bool{"worker": {" b@m ": true, "a@m": false, " ": true}, "reviewer": {}},
		ClaudeAutoMemory: map[string]bool{"worker": false, "reviewer": true},
	}
	if p, off := c.ClaudeLaunchFor("worker"); p != `{"a@m":false,"b@m":true}` || !off {
		t.Fatalf("worker = %q %v", p, off)
	}
	if p, off := c.ClaudeLaunchFor("reviewer"); p != "" || off {
		t.Fatalf("reviewer = %q %v", p, off)
	}
	if p, off := (ProjectConfig{}).ClaudeLaunchFor("orchestrator"); p != "" || off {
		t.Fatalf("unset = %q %v", p, off)
	}
}
