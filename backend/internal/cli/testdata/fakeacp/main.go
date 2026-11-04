// Command fakeacp is a scripted ACP agent for the daemon-restart e2e test. It
// logs every prompt it receives to $TALLPINE_FAKEACP_DIR/prompts.log as
// "<cwd>\t<text>" and answers according to the prompt text:
//
//	HOLD-<name>  parks the turn until $TALLPINE_FAKEACP_DIR/release-<name> exists;
//	             the file's content "fail" ends the turn with a provider error
//	             (-32603 internal error; -32000 would read as "sign in again",
//	             which is a different, auth-wait path with no turn_ended notice).
//	anything else ends the turn immediately.
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	out   = json.NewEncoder(os.Stdout)
	outMu sync.Mutex
	dir   = os.Getenv("TALLPINE_FAKEACP_DIR")
	hold  = regexp.MustCompile(`HOLD-[A-Za-z0-9]+`)
)

func send(v any) {
	outMu.Lock()
	defer outMu.Unlock()
	_ = out.Encode(v)
}

func reply(id json.RawMessage, result any) {
	send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func main() {
	cwd, _ := os.Getwd()
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 16<<20)
	for scanner.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				SessionID string `json:"sessionId"`
				Cwd       string `json:"cwd"`
				Prompt    []struct {
					Text string `json:"text"`
				} `json:"prompt"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.Method == "" {
			continue
		}
		sid := "fake-" + filepath.Base(cwd)
		switch req.Method {
		case "initialize":
			reply(req.ID, map[string]any{"protocolVersion": 1, "authMethods": []any{},
				"agentCapabilities": map[string]any{"sessionCapabilities": map[string]any{"resume": map[string]any{}}}})
		case "session/new":
			reply(req.ID, map[string]any{"sessionId": sid})
		case "session/prompt":
			var parts []string
			for _, p := range req.Params.Prompt {
				parts = append(parts, p.Text)
			}
			go prompt(req.ID, req.Params.SessionID, cwd, strings.Join(parts, "\n"))
		case "session/load", "session/resume":
			stallOnce()
			reply(req.ID, map[string]any{})
		default:
			if len(req.ID) > 0 {
				reply(req.ID, map[string]any{})
			}
		}
	}
}

// stallOnce hangs the first session/load or session/resume after
// $TALLPINE_FAKEACP_DIR/stall-load appears, across agent processes, so the daemon's
// handshake timeout fires once and the retry succeeds.
func stallOnce() {
	if _, err := os.Stat(filepath.Join(dir, "stall-load")); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "stalled"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_ = f.Close()
	time.Sleep(time.Hour)
}

func prompt(id json.RawMessage, sid, cwd, text string) {
	line := cwd + "\t" + strings.ReplaceAll(text, "\n", " ⏎ ") + "\n"
	if f, err := os.OpenFile(filepath.Join(dir, "prompts.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.WriteString(line)
		_ = f.Close()
	}
	fail := false
	if name := hold.FindString(text); name != "" && !strings.Contains(cwd, "background-tasks") {
		release := filepath.Join(dir, "release-"+strings.TrimPrefix(name, "HOLD-"))
		for {
			if b, err := os.ReadFile(release); err == nil {
				fail = strings.TrimSpace(string(b)) == "fail"
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if fail {
		send(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32603, "message": "scripted provider failure"}})
		return
	}
	send(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
		"sessionId": sid, "update": map[string]any{"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{"type": "text", "text": "ok"}}}})
	reply(id, map[string]any{"stopReason": "end_turn"})
}
