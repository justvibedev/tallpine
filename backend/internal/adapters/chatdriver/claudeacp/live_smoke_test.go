package claudeacp

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/contextwindow"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// liveTurn is what one real turn produced, as Tallpine's controller would see it.
type liveTurn struct {
	answer     string
	state      domain.TurnState
	commands   int      // activity.started of kind command
	activity   []string // activity details, to find a tool's output
	approvals  int
	inputs     int
	deltaCount int
}

// runLiveTurn sends one turn and drives it to its end. onEvent may act on
// events (approve, answer, interrupt); an approval it does not handle is
// allowed once so a tool call cannot stall the run.
func runLiveTurn(
	ctx context.Context, t *testing.T, conv ports.ChatConversation, id, prompt string,
	onEvent func(ports.ChatEvent, ports.ChatTurnRef) bool,
) liveTurn {
	t.Helper()
	return runLiveMessage(ctx, t, conv, ports.ChatUserMessage{Text: prompt, ClientMessageID: id, Origin: domain.MessageOriginHuman}, onEvent)
}

func runLiveMessage(
	ctx context.Context, t *testing.T, conv ports.ChatConversation, message ports.ChatUserMessage,
	onEvent func(ports.ChatEvent, ports.ChatTurnRef) bool,
) liveTurn {
	t.Helper()
	id := message.ClientMessageID
	ref, err := conv.SendTurn(ctx, message)
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	var out liveTurn
	var answer strings.Builder
	for {
		select {
		case event, ok := <-conv.Events():
			if !ok {
				t.Fatalf("controller closed before the turn ended; answer=%q", answer.String())
			}
			handled := onEvent != nil && onEvent(event, ref)
			switch event.Kind {
			case ports.ChatEventMessageDelta:
				answer.WriteString(event.Delta)
				out.deltaCount++
			case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted, ports.ChatEventCommandOutputDelta:
				if event.Kind == ports.ChatEventActivityStarted && event.ActivityKind == domain.ActivityKindCommand {
					out.commands++
				}
				out.activity = append(out.activity, event.Summary+" "+string(event.Detail)+" "+event.Delta)
			case ports.ChatEventApprovalRequested:
				out.approvals++
				if !handled {
					for _, option := range event.Decisions {
						if option.Kind == ports.ChatDecisionAllowOnce {
							_ = conv.ResolveRequest(ctx, event.RequestID, ports.ChatDecision{ID: option.ID})
							break
						}
					}
				}
			case ports.ChatEventInputRequested:
				out.inputs++
			case ports.ChatEventTurnCompleted:
				out.answer, out.state = answer.String(), event.TurnState
				if acknowledger, ok := conv.(ports.ChatProviderEventAcknowledger); ok {
					_ = acknowledger.AcknowledgeProviderEvent(context.Background(), event.ProviderEventID)
				}
				return out
			}
		case <-ctx.Done():
			t.Fatalf("live turn %s timed out: %v; answer=%q", id, ctx.Err(), answer.String())
		}
	}
}

// TestLiveClaudeACPSmoke drives the packaged bridge and the user's Claude Code
// through Tallpine's real ACP client: a tool call, AskUserQuestion, stop, a model
// switch and a resume. It spends a handful of small real turns, so it runs only
// with TALLPINE_LIVE_CLAUDE_ACP=1 and TALLPINE_TESTENV_ACTIVE=1 (the test sandbox would
// otherwise hide the login). Run it after changing the pinned
// claude-agent-acp release or any bridge patch.
// startLiveConversation starts a smoke-test conversation and arranges for its
// persistent host to be shut down when the test ends.
func startLiveConversation(ctx context.Context, t *testing.T, driver ports.ChatDriver, session string) (conv ports.ChatConversation, workspace, dataDir string) {
	t.Helper()
	workspace, dataDir = t.TempDir(), t.TempDir()
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, session) })
	conv, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID(session), DataDir: dataDir, WorkspacePath: workspace,
		SystemPrompt: "This is a Tallpine live smoke test. Keep answers short.",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return conv, workspace, dataDir
}

// hibernateAndResumeLive resumes the conversation in a replacement bridge process.
func hibernateAndResumeLive(ctx context.Context, t *testing.T, driver ports.ChatDriver, conv ports.ChatConversation, session, dataDir, workspace string) ports.ChatConversation {
	t.Helper()
	providerID := conv.ProviderConversationID()
	if err := conv.(ports.ChatProviderHibernator).Hibernate(); err != nil {
		t.Fatalf("Hibernate: %v", err)
	}
	resumed, err := driver.Resume(ctx, ports.ChatResumeConfig{
		SessionID: domain.SessionID(session), ProviderConversationID: providerID, DataDir: dataDir, WorkspacePath: workspace,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	return resumed
}

func TestLiveClaudeACPSmoke(t *testing.T) {
	if os.Getenv("TALLPINE_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set TALLPINE_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	const session = "live-claude-smoke"
	conv, workspace, dataDir := startLiveConversation(ctx, t, driver, session)

	// 1. A tool call: the command's output reaches Tallpine as activity.
	tool := runLiveTurn(ctx, t, conv, "smoke-tool",
		"Use the Bash tool to run exactly: echo Tallpine-SMOKE-OK . Then reply with only its output.", nil)
	if tool.state != domain.TurnStateCompleted || !strings.Contains(tool.answer, "Tallpine-SMOKE-OK") {
		t.Fatalf("tool turn: state %q answer %q", tool.state, tool.answer)
	}
	if tool.commands == 0 || !strings.Contains(strings.Join(tool.activity, "\n"), "Tallpine-SMOKE-OK") {
		t.Fatalf("tool turn: command activity missing (commands=%d, activity=%q)", tool.commands, tool.activity)
	}
	t.Logf("tool call: commands=%d approvals=%d", tool.commands, tool.approvals)

	// 2. AskUserQuestion arrives as a request Tallpine can answer, and the answer is used.
	ask := runLiveTurn(ctx, t, conv, "smoke-ask",
		"Use the AskUserQuestion tool once: header 'Color', question 'Which color?', options 'red' and 'blue'. After I answer, reply with only the chosen color.",
		func(event ports.ChatEvent, _ ports.ChatTurnRef) bool {
			if event.Kind != ports.ChatEventInputRequested || event.Input == nil {
				return false
			}
			// A form: pick "blue" in the first select question. The free-text
			// "custom answer" companion is marked and left empty.
			properties, _ := event.Input.Schema["properties"].(map[string]any)
			for name, raw := range properties {
				property, _ := raw.(map[string]any)
				if _, isSelect := property["oneOf"]; !isSelect {
					continue
				}
				err := conv.(ports.ChatInputResponder).ResolveInput(ctx, event.RequestID, ports.ChatInputResponse{
					Action: ports.ChatInputActionAccept, Content: map[string]any{name: "blue"},
				})
				if err != nil {
					t.Errorf("ResolveInput: %v", err)
				}
				return true
			}
			return false
		})
	if ask.state != domain.TurnStateCompleted || ask.inputs == 0 || !strings.Contains(strings.ToLower(ask.answer), "blue") {
		t.Fatalf("ask turn: state %q approvals %d inputs %d answer %q", ask.state, ask.approvals, ask.inputs, ask.answer)
	}
	t.Logf("AskUserQuestion: approvals=%d inputs=%d answer=%q", ask.approvals, ask.inputs, ask.answer)

	// 3. Stop: an interrupt ends a running turn without completing it.
	stopped := false
	stop := runLiveTurn(ctx, t, conv, "smoke-stop", "Count from 1 to 300, one number per line, no other text.",
		func(event ports.ChatEvent, ref ports.ChatTurnRef) bool {
			if event.Kind == ports.ChatEventMessageDelta && !stopped {
				stopped = true
				if err := conv.Interrupt(ctx, ref.ProviderTurnID); err != nil {
					t.Errorf("Interrupt: %v", err)
				}
			}
			return false
		})
	if !stopped || stop.state == domain.TurnStateCompleted && stop.deltaCount > 250 {
		t.Fatalf("stop turn: stopped=%v state=%q deltas=%d", stopped, stop.state, stop.deltaCount)
	}
	t.Logf("stop: state=%q deltas=%d", stop.state, stop.deltaCount)

	// 4. Model switch through the standard config option.
	options, err := conv.(ports.ChatConfigOptionController).ListConfigOptions(ctx)
	if err != nil {
		t.Fatalf("ListConfigOptions: %v", err)
	}
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID+"("+option.Category+")")
	}
	t.Logf("config options: %v", ids)
	for _, option := range options {
		if option.Category != "model" {
			continue
		}
		for _, choice := range option.Choices {
			if choice.Value == option.Current.Select {
				continue
			}
			updated, err := conv.(ports.ChatConfigOptionController).SetConfigOption(ctx, option.ID, ports.ChatConfigOptionValue{Select: choice.Value})
			if err != nil {
				t.Fatalf("SetConfigOption(%s=%s): %v", option.ID, choice.Value, err)
			}
			switched := false
			for _, after := range updated {
				switched = switched || after.ID == option.ID && after.Current.Select == choice.Value
			}
			if !switched {
				t.Fatalf("model option did not move to %q: %+v", choice.Value, updated)
			}
			t.Logf("model switch: %q -> %q", option.Current.Select, choice.Value)
			break
		}
		break
	}
	after := runLiveTurn(ctx, t, conv, "smoke-after-switch", "Reply with only: ok", nil)
	if after.state != domain.TurnStateCompleted || !strings.Contains(strings.ToLower(after.answer), "ok") {
		t.Fatalf("turn after model switch: state %q answer %q", after.state, after.answer)
	}

	// 5. Resume in a replacement bridge process keeps the conversation.
	conv = hibernateAndResumeLive(ctx, t, driver, conv, session, dataDir, workspace)
	defer conv.(ports.ChatProviderTerminator).Terminate()
	resumed := runLiveTurn(ctx, t, conv, "smoke-resumed", "What exact string did the Bash command print earlier? Reply with only that string.", nil)
	if !strings.Contains(resumed.answer, "Tallpine-SMOKE-OK") {
		t.Fatalf("resumed conversation lost its history: %q", resumed.answer)
	}
}

// redSquarePNG is a solid red image the model can name.
func redSquarePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for x := range 96 {
		for y := range 96 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestLiveClaudeACPMedia covers what changes most between bridge releases:
// image prompts and image tool results (Tallpine must not store the bytes), a native
// subagent's nested activity, and the history a session/load replay returns.
func TestLiveClaudeACPMedia(t *testing.T) {
	if os.Getenv("TALLPINE_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set TALLPINE_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	const session = "live-claude-media"
	conv, workspace, dataDir := startLiveConversation(ctx, t, driver, session)
	if !conv.Capabilities().Has(ports.ChatCapabilityImages) {
		t.Fatalf("the bridge no longer advertises image prompts: %v", conv.Capabilities())
	}
	pngBytes := redSquarePNG(t)
	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	leaked := func(turn liveTurn) bool {
		return strings.Contains(strings.Join(turn.activity, "\n"), encoded[:80])
	}

	// 1a. An image in the prompt reaches the model.
	prompted := runLiveMessage(ctx, t, conv, ports.ChatUserMessage{
		Text: "What single color fills this image? Reply with one lowercase word.", ClientMessageID: "media-prompt",
		Origin:  domain.MessageOriginHuman,
		Content: []ports.ChatContent{{Type: "image", Data: encoded, MIMEType: "image/png"}},
	}, nil)
	if prompted.state != domain.TurnStateCompleted || !strings.Contains(strings.ToLower(prompted.answer), "red") {
		t.Fatalf("image prompt: state %q answer %q", prompted.state, prompted.answer)
	}
	// 1b. An image in a tool result: the model sees it, Tallpine's activity holds only a stub.
	if err := os.WriteFile(filepath.Join(workspace, "square.png"), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	read := runLiveTurn(ctx, t, conv, "media-read",
		"Use the Read tool on square.png in the working directory, then reply with one lowercase word: its color.", nil)
	if read.state != domain.TurnStateCompleted || !strings.Contains(strings.ToLower(read.answer), "red") {
		t.Fatalf("image tool result: state %q answer %q", read.state, read.answer)
	}
	if leaked(read) || leaked(prompted) {
		t.Fatal("image bytes reached Tallpine's activity detail")
	}
	t.Logf("image tool result: activities=%d, stub present=%v", len(read.activity),
		strings.Contains(strings.Join(read.activity, "\n"), `"bytes"`))

	// 2. A native subagent's nested activity is linked to its parent.
	sub := runLiveTurn(ctx, t, conv, "media-subagent",
		"Use the Agent tool (subagent_type general-purpose) to run a subagent whose only job is to run the Bash command `echo SUB-OK` and report its output. Then reply with only that report.", nil)
	joined := strings.Join(sub.activity, "\n")
	if sub.state != domain.TurnStateCompleted || !strings.Contains(sub.answer, "SUB-OK") {
		t.Fatalf("subagent turn: state %q answer %q", sub.state, sub.answer)
	}
	if !strings.Contains(joined, "parentProviderItemId") {
		t.Fatalf("no activity was linked to a parent tool call (subagent transcript lost): %.600s", joined)
	}
	if !strings.Contains(joined, `"nestedAgent":true`) {
		t.Fatalf("the Agent tool call is not marked as a nested agent: %.600s", joined)
	}

	// 3. History: several turns, a replacement bridge, and session/load's replay
	// holds each user message exactly once.
	markers := []string{"H1-alpha", "H2-beta", "H3-gamma"}
	for i, marker := range markers {
		turn := runLiveTurn(ctx, t, conv, "hist-"+marker, "Note this tag and reply only ok: "+marker, nil)
		if turn.state != domain.TurnStateCompleted {
			t.Fatalf("history turn %d: %q", i, turn.state)
		}
	}
	conv = hibernateAndResumeLive(ctx, t, driver, conv, session, dataDir, workspace)
	defer conv.(ports.ChatProviderTerminator).Terminate()
	events, err := conv.(ports.ChatHistoryReader).ReadHistory(ctx)
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	userMessages := 0
	counts := map[string]int{}
	for _, event := range events {
		if event.Kind != ports.ChatEventUserMessageCompleted {
			continue
		}
		userMessages++
		for _, marker := range markers {
			if strings.Contains(event.Text, marker) {
				counts[marker]++
			}
		}
	}
	for _, marker := range markers {
		if counts[marker] != 1 {
			t.Errorf("history has %q %d times, want exactly once (events=%d, user messages=%d)", marker, counts[marker], len(events), userMessages)
		}
	}
	// Every real prompt of the session: image, read, subagent and 3 tagged turns,
	// plus the two earlier prompts that ran on this conversation.
	t.Logf("history: events=%d userMessages=%d counts=%v", len(events), userMessages, counts)
	if userMessages != 6 {
		t.Errorf("user messages in replay = %d, want 6 (image, read, subagent, 3 tagged)", userMessages)
	}
}

// TestLiveClaudeACPContextBreakdown asks a real Claude Code session what fills its
// window, and checks the compaction threshold it reports follows a live change of
// the window: the proof that the setting reaches the CLI, not just the bridge.
func TestLiveClaudeACPContextBreakdown(t *testing.T) {
	if os.Getenv("TALLPINE_LIVE_CLAUDE_ACP") != "1" {
		t.Skip("set TALLPINE_LIVE_CLAUDE_ACP=1 to run against the local Claude Code account")
	}
	driver := New(claudecode.New(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	if _, err := driver.Probe(ctx); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	dataDir := t.TempDir()
	const session = "live-claude-breakdown"
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, session) })
	conv, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID(session), DataDir: dataDir, WorkspacePath: t.TempDir(),
		Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "400000"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.(ports.ChatProviderTerminator).Terminate()
	if turn := runLiveTurn(ctx, t, conv, "bd-1", "Use the Bash tool to run `echo BREAKDOWN-OK`, then reply only ok.", nil); turn.state != domain.TurnStateCompleted {
		t.Fatalf("turn: %q", turn.state)
	}
	reader := conv.(ports.ChatContextBreakdownReader)
	first, supported, err := reader.ContextBreakdown(ctx)
	if err != nil || !supported {
		t.Fatalf("ContextBreakdown: supported=%v err=%v", supported, err)
	}
	byID := map[string]domain.ContextCategory{}
	var usedSum int64
	for _, category := range first.Categories {
		byID[category.ID] = category
		if category.Kind == domain.ContextKindUsed {
			usedSum += category.Tokens
		}
		t.Logf("category %-22s %-9s %7d", category.ID, category.Kind, category.Tokens)
	}
	t.Logf("total=%d max=%d threshold=%d usedSum=%d messages=%+v memoryFiles=%d", first.TotalTokens, first.MaxTokens,
		first.AutoCompactThreshold, usedSum, first.Messages, len(first.MemoryFiles))
	if byID["messages"].Tokens <= 0 || byID["system_prompt"].Tokens <= 0 {
		t.Fatalf("expected non-zero messages and system prompt rows: %+v", byID)
	}
	if first.Messages == nil || first.Messages.ToolResults <= 0 {
		t.Fatalf("the Bash call left no tool-result tokens: %+v", first.Messages)
	}
	if first.AutoCompactThreshold <= 0 {
		t.Fatalf("no compaction threshold reported: %+v", first)
	}

	// A live change: the bridge may only say "applied" if the threshold the CLI
	// itself reports moved. Claude Code 2.1.312 keeps its launch window, so this
	// run expects applied=false and an unchanged threshold; a CLI that honors it
	// must show applied=true and a lower threshold. Anything else is the lie this
	// check exists to catch.
	applied, err := conv.(ports.ChatCompactionWindowSetter).SetAutoCompactWindow(ctx, 300000)
	if err != nil {
		t.Fatalf("SetAutoCompactWindow: %v", err)
	}
	second, _, err := reader.ContextBreakdown(ctx)
	if err != nil {
		t.Fatal(err)
	}
	moved := second.AutoCompactThreshold < first.AutoCompactThreshold
	t.Logf("live change 400000 -> 300000: applied=%v threshold %d -> %d (moved=%v)", applied, first.AutoCompactThreshold, second.AutoCompactThreshold, moved)
	if applied != moved {
		t.Fatalf("bridge said applied=%v but the CLI's threshold moved=%v (%d -> %d)", applied, moved, first.AutoCompactThreshold, second.AutoCompactThreshold)
	}

	// A fresh launch with the window does take effect: that is what a restart does.
	restarted, err := driver.Start(ctx, ports.ChatStartConfig{
		SessionID: domain.SessionID(session + "-restart"), DataDir: dataDir, WorkspacePath: t.TempDir(),
		Env: map[string]string{contextwindow.ClaudeAutoCompactWindowEnv: "300000"},
	})
	if err != nil {
		t.Fatalf("Start (restart): %v", err)
	}
	defer restarted.(ports.ChatProviderTerminator).Terminate()
	t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), dataDir, session+"-restart") })
	third, supported, err := restarted.(ports.ChatContextBreakdownReader).ContextBreakdown(ctx)
	if err != nil || !supported {
		t.Fatalf("ContextBreakdown after relaunch: %v %v", supported, err)
	}
	t.Logf("fresh launch with 300000: max=%d threshold=%d", third.MaxTokens, third.AutoCompactThreshold)
	if third.MaxTokens != 300000 || third.AutoCompactThreshold >= first.AutoCompactThreshold {
		t.Fatalf("a launch with the window did not change the CLI's window: %+v", third)
	}
}
