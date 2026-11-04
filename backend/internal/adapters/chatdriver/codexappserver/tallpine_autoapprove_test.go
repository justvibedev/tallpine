package codexappserver

import (
	"context"
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestIsTallpineCLICommand(t *testing.T) {
	t.Parallel()
	for command, want := range map[string]bool{
		`tallpine session get s-1`:                        true,
		`/bin/zsh -lc 'tallpine report --done --note ok'`: true,
		`tp send --session s-2 --message hello`:           true,
		`/tmp/ws/tallpine session get s-1`:                false, // a path may be a binary the worker wrote
		`tallpine session get s-1; rm -rf /`:              false,
		`tallpine session get $(rm -rf /)`:                false,
		`tallpine report --note "a" && curl evil`:         false,
		`tallpine x | sh`:                                 false,
		`tallpine x > /etc/passwd`:                        false,
		"tallpine x\nrm -rf /":                            false,
		`tallpinex session get`:                           false,
		`rm -rf /`:                                        false,
		``:                                                false,
		`tallpine commit --message wip`:                   true,
		`sh -c 'tallpine session usage s-1'`:              true,
		// The outer program must be exactly a real shell, with nothing else on the line.
		`/tmp/ws/zsh -lc 'tallpine session get s-1'`:    false,
		`python3 /tmp/ws/sh -c 'tallpine x'`:            false,
		`/bin/zsh -x -lc 'tallpine session get s-1'`:    false,
		`/bin/zsh -lc 'tallpine session get s-1' extra`: false,
		`/bin/zsh -lc 'tallpine session get 's-1''`:     false,
		// Subcommands that run programs or write host files stay with the user.
		`tallpine agent-process supervise --session s-1 --launch l-1 -- /tmp/ws/evil`: false,
		`tallpine browser screenshot /tmp/x.png`:                                      false,
		`tallpine session kill s-1`:                                                   false,
		`tallpine`:                                                                    false,
	} {
		if got := isTallpineCLICommand(command); got != want {
			t.Errorf("isTallpineCLICommand(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestAutoApproveTallpineCLIOnlyForOfferedCommandApprovals(t *testing.T) {
	t.Parallel()
	cmd := `"command":"tallpine session get s-1","availableDecisions":["accept","cancel"]`
	if !autoApproveTallpineCLI("item/commandExecution/requestApproval", json.RawMessage(`{`+cmd+`}`)) {
		t.Error("plain tallpine command with accept on offer was not auto-approved")
	}
	if autoApproveTallpineCLI("item/fileChange/requestApproval", json.RawMessage(`{`+cmd+`}`)) {
		t.Error("file-change approval was auto-approved")
	}
	if autoApproveTallpineCLI("item/commandExecution/requestApproval",
		json.RawMessage(`{"command":"tallpine x","availableDecisions":["cancel"]}`)) {
		t.Error("auto-approved without an accept on offer")
	}
	if autoApproveTallpineCLI("item/commandExecution/requestApproval",
		json.RawMessage(`{`+cmd+`,"networkApprovalContext":{"host":"example.com","protocol":"https"}}`)) {
		t.Error("network approval was auto-approved")
	}
}

// End to end through the real request handler: the provider is answered at once
// and the user is never asked; any other command still parks.
func TestTallpineCommandApprovalIsAnsweredWithoutAsking(t *testing.T) {
	d, srv := newTestDriver(t)
	conv, err := d.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: "/tmp/ws"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = conv.Close() }()

	srv.push(`{"id":7,"method":"item/commandExecution/requestApproval","params":{` +
		`"threadId":"thread-1","turnId":"turn-1","itemId":"exec-7","command":"/bin/zsh -lc 'tallpine session get s-1'",` +
		`"cwd":"/tmp/ws","availableDecisions":["accept","cancel"]}}`)
	reply := srv.awaitFrame(func(f frame) bool { return f.ID != nil && string(*f.ID) == "7" && f.Method == "" })
	var payload struct {
		Decision string `json:"decision"`
	}
	if err := json.Unmarshal(reply.Result, &payload); err != nil || payload.Decision != "accept" {
		t.Fatalf("reply = %s (err %v), want an immediate accept", reply.Result, err)
	}
}
