package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/commanddetail"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ResolveRequest answers a parked approval or user-input request.
//
// The decision is checked against the set the provider offered for THIS request,
// and the request stays parked unless a valid answer is actually going through.
// Both halves matter: forwarding an invented decision is consent Tallpine made up, and
// consuming the request on a bad one would leave the user's real answer with
// nothing left to answer while the provider waits out its timeout.
func (c *conversation) ResolveRequest(ctx context.Context, requestID string, decision ports.ChatDecision) error {
	requestID = c.nativeID(requestID)
	c.mu.Lock()
	parked, ok := c.pending[requestID]
	closed := c.closed
	if closed {
		c.mu.Unlock()
		return errConversationClosed
	}
	if !ok {
		c.mu.Unlock()
		// Already resolved, superseded, or from a previous controller. Refusing is
		// required: a stale card must never resolve a newer request.
		return fmt.Errorf("%w: %q", ports.ErrChatRequestNotPending, requestID)
	}
	reply, err := parked.reply(decision)
	if err != nil {
		c.mu.Unlock()
		return err
	}
	delete(c.pending, requestID)
	c.mu.Unlock()

	select {
	case parked.ch <- reply:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// parkedRequest is one server-to-client request waiting on a person, together
// with the decisions the provider said were valid for it.
//
// Keeping the provider's own payload for each decision is what makes structured
// decisions answerable at all: some arrive as objects carrying parameters
// (acceptWithExecpolicyAmendment), and a client that only knows the id cannot
// reconstruct them. Tallpine echoes what the provider sent rather than rebuilding it.
type parkedRequest struct {
	ch      chan ports.ChatDecision
	method  string
	offered map[string]json.RawMessage
}

// reply resolves a client decision into the payload to send back.
func (p *parkedRequest) reply(decision ports.ChatDecision) (ports.ChatDecision, error) {
	if p.method == "item/tool/requestUserInput" {
		// Not a decision but an answer: the provider offers questions, not options,
		// so there is no set to check it against.
		return decision, nil
	}
	raw, ok := p.offered[decision.ID]
	if !ok {
		return ports.ChatDecision{}, fmt.Errorf("%w: %q (offered: %s)",
			ports.ErrChatDecisionNotOffered, decision.ID, strings.Join(p.offeredIDs(), ", "))
	}
	// The provider's own encoding of the decision wins over whatever the client
	// sent, so an object-shaped decision round-trips exactly.
	decision.Raw = raw
	return decision, nil
}

func (p *parkedRequest) offeredIDs() []string {
	ids := make([]string, 0, len(p.offered))
	for id := range p.offered {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// handleServerRequest parks a provider request until a decision arrives.
//
// The provider blocks its turn on the reply, so this deliberately waits rather
// than answering immediately. Everything Tallpine does not model is refused with an
// error, never with a fabricated decision.
func (c *conversation) handleServerRequest(ctx context.Context, req serverRequest) (any, error) {
	switch req.Method {
	case codexproto.MethodAccountChatgptAuthTokensRefresh:
		return nil, c.reportAuthRefreshRequest(req.Params)
	case codexproto.MethodItemToolCall:
		return nil, c.refuseDynamicToolCall(req.Params)
	}

	kind, known := approvalMethods[req.Method]
	if !known {
		c.log.Warn("refusing unmodelled app-server request", "method", req.Method)
		return nil, fmt.Errorf("unsupported request %s", req.Method)
	}
	if c.disableNetwork {
		return nil, errors.New("background task does not permit approval or input requests")
	}

	requestID := rawID(req.ID)
	if requestID == "" {
		return nil, errors.New("server request carried no id")
	}
	if autoApproveTallpineCLI(req.Method, req.Params) {
		c.log.Info("auto-approved tallpine CLI command outside the sandbox", "request", requestID)
		return map[string]any{"decision": "accept"}, nil
	}

	decisions, summary, detail, turnID := parseApproval(req.Method, req.Params)

	offered := make(map[string]json.RawMessage, len(decisions))
	for _, option := range decisions {
		offered[option.ID] = option.Raw
	}

	ch := make(chan ports.ChatDecision, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errConversationClosed
	}
	c.pending[requestID] = &parkedRequest{ch: ch, method: req.Method, offered: offered}
	c.mu.Unlock()

	c.emit(ports.ChatEvent{
		Kind:           ports.ChatEventApprovalRequested,
		ProviderTurnID: turnID,
		ProviderItemID: requestID,
		RequestID:      requestID,
		ActivityKind:   kind,
		ActivityStatus: domain.ActivityStatusPending,
		Summary:        summary,
		Detail:         detail,
		Decisions:      decisions,
	})

	select {
	case decision := <-ch:
		return approvalReply(req.Method, decision), nil

	case <-time.After(approvalWait):
		c.discardPending(requestID)
		return nil, errors.New("approval timed out without a decision")

	case <-ctx.Done():
		c.discardPending(requestID)
		return nil, ctx.Err()
	}
}

// reportAuthRefreshRequest surfaces the provider asking for ChatGPT credentials and
// returns the refusal to send back.
//
// Tallpine does not hold provider credentials. Codex owns its own ChatGPT OAuth tokens
// (authMode `chatgpt`) and refreshes them itself; this request belongs to authMode
// `chatgptAuthTokens`, where an external host app supplies them, and the generated
// schema marks that mode OpenAI-internal. So the honest answer is an error — but the
// error alone would only reach a log, and what the user needs to know is that the
// session has stopped working for a reason no retry will fix.
//
// NEVER OBSERVED live: reaching it requires the provider to take a 401 while running
// in a mode Tallpine does not use, which a test cannot arrange without breaking real auth.
func (c *conversation) reportAuthRefreshRequest(params json.RawMessage) error {
	var p codexproto.ChatgptAuthTokensRefreshParams
	// A payload this build cannot parse still means the same thing: the provider
	// asked for credentials Tallpine cannot supply.
	_ = json.Unmarshal(params, &p)

	reason := string(p.Reason)
	if reason == "" {
		reason = "unauthorized"
	}
	c.log.Warn("app-server asked for ChatGPT auth tokens Tallpine does not hold", "reason", reason)
	c.emit(ports.ChatEvent{
		Kind: ports.ChatEventAccountChanged,
		Account: &ports.ChatAccount{
			ReauthRequired: true,
			ReauthReason:   reason,
		},
	})
	return fmt.Errorf("%w: this client does not supply ChatGPT auth tokens (reason: %s)",
		ports.ErrChatAuthRequired, reason)
}

// refuseDynamicToolCall declines a request to run a tool Tallpine never offered.
//
// `item/tool/call` asks the CLIENT to execute a tool the client declared during
// initialize. Tallpine declares none, so a well-behaved provider will never send this and
// one that does is asking Tallpine to run something it has no definition for. Refusing is
// the only safe answer: inventing a result would feed the model a fabrication.
func (c *conversation) refuseDynamicToolCall(params json.RawMessage) error {
	var p codexproto.DynamicToolCallParams
	_ = json.Unmarshal(params, &p)
	c.log.Warn("refusing dynamic tool call: Tallpine declares no client-side tools",
		"tool", p.Tool, "callId", p.CallID)
	return fmt.Errorf("client declares no tools; %q is not available", p.Tool)
}

func (c *conversation) discardPending(requestID string) {
	c.mu.Lock()
	delete(c.pending, requestID)
	c.mu.Unlock()
}

// failPendingApprovals unblocks every parked handler when the controller ends.
func (c *conversation) failPendingApprovals() {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]*parkedRequest{}
	c.closed = true
	c.mu.Unlock()
	for _, parked := range pending {
		close(parked.ch)
	}
}

// approvalPayload is the subset of an approval request Tallpine renders.
type approvalPayload struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Command  string `json:"command"`
	Cwd      string `json:"cwd"`
	Reason   string `json:"reason"`
	// AvailableDecisions is authoritative: the provider varies the offered set
	// per request and does not always offer a plain decline, so a client must
	// render from this rather than a fixed set of buttons.
	AvailableDecisions []json.RawMessage `json:"availableDecisions"`
	Questions          []struct {
		ID      string `json:"id"`
		Prompt  string `json:"prompt"`
		Options []struct {
			Label string `json:"label"`
		} `json:"options"`
	} `json:"questions"`
}

// parseApproval extracts the decisions, label, and neutral detail for a request.
func parseApproval(method string, params json.RawMessage) ([]ports.ChatDecisionOption, string, []byte, string) {
	var p approvalPayload
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, "Approval required", nil, ""
	}

	options := make([]ports.ChatDecisionOption, 0, len(p.AvailableDecisions))
	for _, raw := range p.AvailableDecisions {
		if opt, ok := decisionOption(raw); ok {
			options = append(options, opt)
		}
	}

	summary := "Approval required"
	switch {
	case p.Command != "":
		summary = "Run " + commandSummary(p.Command)
	case method == "item/fileChange/requestApproval":
		summary = "Apply file changes"
	case method == "item/tool/requestUserInput" && len(p.Questions) > 0:
		summary = p.Questions[0].Prompt
	case p.Reason != "":
		summary = p.Reason
	}

	detail := map[string]any{"method": method}
	if p.Command != "" {
		detail["command"] = commanddetail.UnwrapShell(p.Command)
		detail["rawCommand"] = p.Command
	}
	if p.Cwd != "" {
		detail["cwd"] = p.Cwd
	}
	if p.ItemID != "" {
		detail["itemId"] = p.ItemID
	}
	if p.Reason != "" {
		detail["reason"] = p.Reason
	}
	if len(p.Questions) > 0 {
		detail["questions"] = p.Questions
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		encoded = nil
	}
	return options, summary, encoded, p.TurnID
}

// decisionOption reads one entry of availableDecisions, which is either a plain
// string ("accept") or a single-key object carrying parameters
// ({"acceptWithExecpolicyAmendment": {...}}).
func decisionOption(raw json.RawMessage) (ports.ChatDecisionOption, bool) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ports.ChatDecisionOption{}, false
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if asString == "" {
			return ports.ChatDecisionOption{}, false
		}
		return ports.ChatDecisionOption{
			ID: asString, Label: decisionLabel(asString), Kind: codexDecisionKind(asString), Raw: raw,
		}, true
	}

	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err != nil || len(asObject) != 1 {
		return ports.ChatDecisionOption{}, false
	}
	for key := range asObject {
		return ports.ChatDecisionOption{
			ID: key, Label: decisionLabel(key), Kind: codexDecisionKind(key), Raw: raw,
		}, true
	}
	return ports.ChatDecisionOption{}, false
}

func codexDecisionKind(id string) ports.ChatDecisionKind {
	switch id {
	case "accept":
		return ports.ChatDecisionAllowOnce
	case "acceptForSession", "acceptWithExecpolicyAmendment":
		return ports.ChatDecisionAllowAlways
	case "decline", "cancel":
		return ports.ChatDecisionRejectOnce
	default:
		return ""
	}
}

// decisionLabel gives known decision ids readable text. An unknown id falls back
// to its own name rather than being hidden, so a new provider decision still
// renders as a usable button.
func decisionLabel(id string) string {
	switch id {
	case "accept":
		return "Approve"
	case "acceptForSession":
		return "Approve for this session"
	case "acceptWithExecpolicyAmendment":
		return "Approve and remember this command"
	case "decline":
		return "Decline"
	case "cancel":
		return "Cancel"
	default:
		return id
	}
}

// approvalReply builds the provider response for a decision. A decision carrying
// Raw is echoed verbatim so the structured forms round-trip exactly.
func approvalReply(method string, decision ports.ChatDecision) any {
	if method == "item/tool/requestUserInput" {
		// User-input replies are answers, not decisions; the raw payload is the
		// whole response body.
		if len(decision.Raw) > 0 {
			return json.RawMessage(decision.Raw)
		}
		return map[string]any{"answers": map[string]any{}}
	}
	if len(decision.Raw) > 0 {
		return map[string]any{"decision": json.RawMessage(decision.Raw)}
	}
	return map[string]any{"decision": decision.ID}
}
