package codexappserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// contextPositionFrom reports the conversation's context position, or ok=false for
// a notification that is not a token-usage report.
func contextPositionFrom(n notification) (used, window int64, ok bool) {
	if n.Method != codexproto.MethodThreadTokenUsageUpdated {
		return 0, 0, false
	}
	var p contextEnvelope
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return 0, 0, false
	}
	if p.TokenUsage.ModelContextWindow != nil {
		window = *p.TokenUsage.ModelContextWindow
	}
	return p.TokenUsage.Last.TotalTokens, window, true
}

func accountUpdated(n notification, _ time.Time) []ports.ChatEvent {
	// The account's auth mode or plan changed. NEVER OBSERVED in a probe: it
	// fires on login, logout, and plan changes, none of which a test may do to a
	// real account. The payload is two optional enums, so reading it is
	// unambiguous even unverified.
	var p codexproto.AccountUpdatedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	account := ports.ChatAccount{}
	if p.AuthMode != nil {
		account.AuthMode = string(*p.AuthMode)
	}
	if p.PlanType != nil {
		account.PlanLabel = string(*p.PlanType)
	}
	if account.AuthMode == "" && account.PlanLabel == "" {
		return nil
	}
	return []ports.ChatEvent{{Kind: ports.ChatEventAccountChanged, Account: &account}}
}

func threadStatusChanged(n notification, _ time.Time) []ports.ChatEvent {
	// VERIFIED live: {"status":{"type":"active","activeFlags":[]}} while a turn
	// runs and {"status":{"type":"idle"}} when it ends.
	//
	// Recorded as thread state and deliberately NOT fed into Tallpine's activity
	// signal. Turn lifecycle already drives that, and a second driver of the same
	// reduction would be two sources of truth for one status.
	var p codexproto.ThreadStatusChangedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	state := ports.ChatThreadState{
		Status:    threadStatusFrom(p.Status.Type),
		WaitingOn: waitingOnFrom(p.Status.ActiveFlags),
	}
	if state.Status == "" {
		return nil
	}
	return []ports.ChatEvent{{Kind: ports.ChatEventThreadState, ThreadState: &state}}
}

func threadArchivedChanged(n notification, _ time.Time) []ports.ChatEvent {
	// VERIFIED live: thread/archive and thread/unarchive each produced their
	// notification. Archiving is the provider's own idea of putting a thread away,
	// and it is reversible, so it is tri-state rather than a one-way marker.
	archived := n.Method == codexproto.MethodThreadArchived
	return []ports.ChatEvent{{
		Kind:        ports.ChatEventThreadState,
		ThreadState: &ports.ChatThreadState{Archived: &archived},
	}}
}

func threadClosed(n notification, _ time.Time) []ports.ChatEvent {
	// NEVER OBSERVED: thread/unsubscribe returned {"status":"unsubscribed"} and
	// emitted nothing. Recorded rather than acted on — tearing a controller down
	// on an unverified notification would turn a provider quirk into a lost
	// session.
	return []ports.ChatEvent{{
		Kind:        ports.ChatEventThreadState,
		ThreadState: &ports.ChatThreadState{Closed: true, Status: domain.ThreadStatusClosed},
	}}
}

func mcpServerStartupStatus(n notification, _ time.Time) []ports.ChatEvent {
	// VERIFIED live: each configured server reports starting, then ready or
	// failed, and re-reports on every turn. Previously dropped as bookkeeping,
	// which left the one explanation for a class of failure unavailable — a tool
	// call that never happened because its server did not start looks, from the
	// timeline alone, like the agent choosing not to use it.
	var p codexproto.McpServerStatusUpdatedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil || p.Name == "" {
		return nil
	}
	server := ports.ChatMCPServer{Name: p.Name, Status: string(p.Status)}
	if p.Error != nil {
		server.Error = *p.Error
	}
	if p.FailureReason != nil {
		server.FailureReason = string(*p.FailureReason)
	}
	return []ports.ChatEvent{{
		Kind:       ports.ChatEventMCPServers,
		MCPServers: []ports.ChatMCPServer{server},
	}}
}

func threadTokenUsageUpdated(n notification, _ time.Time) []ports.ChatEvent {
	var p codexproto.ThreadTokenUsageUpdatedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	// A typed usage event rather than an activity. The provider emits one of
	// these after every tool call, so a timeline row per report is what buried
	// the conversation; this is current state, and the projection overwrites.
	usage := &ports.ChatUsage{
		InputTokens:  p.TokenUsage.Total.InputTokens,
		OutputTokens: p.TokenUsage.Total.OutputTokens,
		CachedTokens: p.TokenUsage.Total.CachedInputTokens,
		TotalTokens:  p.TokenUsage.Total.TotalTokens,
		ContextKnown: true,
		TotalsKnown:  true,
		// Context fullness comes from the LAST request, not the cumulative
		// total: a turn resends the whole conversation, so the last
		// request's size is what is actually occupying the window. Using
		// the running total here would report a conversation as over
		// capacity long before it was.
		ContextUsed: p.TokenUsage.Last.TotalTokens,
	}
	if p.TokenUsage.ModelContextWindow != nil {
		usage.ContextWindow = *p.TokenUsage.ModelContextWindow
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventUsage,
		ProviderTurnID: p.TurnID,
		Usage:          usage,
	}}
}

func accountRateLimitsUpdated(n notification, now time.Time) []ports.ChatEvent {
	var p capacityReadEnvelope
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	limits := chatRateLimitsFromCapacity(capacityObservationFromEnvelope(p, now.UTC(), true), now)
	return []ports.ChatEvent{{Kind: ports.ChatEventRateLimits, RateLimits: &limits}}
}

func threadNameUpdated(n notification, _ time.Time) []ports.ChatEvent {
	var p codexproto.ThreadNameUpdatedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	// A cleared name is reported too, as an empty title. It is a real change to
	// the conversation and the projection needs to see it; what the projection
	// must not do is blank out a label on the strength of it.
	title := ""
	if p.ThreadName != nil {
		title = strings.TrimSpace(*p.ThreadName)
	}
	return []ports.ChatEvent{{
		Kind:  ports.ChatEventThreadRenamed,
		Title: title,
	}}
}

func errorNotification(n notification, _ time.Time) []ports.ChatEvent {
	var p codexproto.ErrorNotification
	if err := json.Unmarshal(n.Params, &p); err == nil &&
		(strings.TrimSpace(p.Error.Message) != "" || p.Error.AdditionalDetails != nil || p.Error.CodexErrorInfo != nil) {
		failure := codexProviderFailure(p.Error)
		if failure == nil {
			return nil
		}
		if p.WillRetry && p.TurnID != "" {
			return []ports.ChatEvent{{
				Kind: ports.ChatEventActivityStarted, ProviderConversationID: p.ThreadID,
				ProviderTurnID: p.TurnID, ProviderItemID: "codex-retry:" + p.ThreadID + ":" + p.TurnID,
				ActivityKind: domain.ActivityKindSystem, ActivityStatus: domain.ActivityStatusRunning,
				Summary: failure.Error(), Detail: json.RawMessage(`{"event":"provider.failure"}`),
			}}
		}
		return []ports.ChatEvent{{
			Kind:                   ports.ChatEventError,
			ProviderTurnID:         p.TurnID,
			ProviderConversationID: p.ThreadID,
			Err:                    failure,
		}}
	}
	return []ports.ChatEvent{{
		Kind: ports.ChatEventError,
		Err:  fmt.Errorf("provider error: %s", truncateForLog(n.Params)),
	}}
}

// Native reasons distinguish quota exhaustion from throttling and authentication
// failures even when the provider's display copy does not name the limit.
func codexProviderFailure(turnErr codexproto.TurnError) error {
	detail := ""
	if turnErr.AdditionalDetails != nil {
		detail = *turnErr.AdditionalDetails
	}
	var cause error
	if turnErr.CodexErrorInfo != nil {
		var reason string
		_ = json.Unmarshal(*turnErr.CodexErrorInfo, &reason)
		switch reason {
		case "unauthorized":
			cause = ports.ErrChatAuthRequired
		case "usageLimitExceeded":
			cause = errCodexUsageLimitExceeded
		}
	}
	if strings.TrimSpace(turnErr.Message) == "" && strings.TrimSpace(detail) == "" && cause == nil {
		return nil
	}
	if strings.TrimSpace(turnErr.Message) == "" && strings.TrimSpace(detail) == "" && errors.Is(cause, errCodexUsageLimitExceeded) {
		return ports.NewChatProviderFailure("Usage limit exceeded", "", cause)
	}
	failure := ports.NewChatProviderFailure(turnErr.Message, detail, cause)
	if errors.Is(cause, errCodexUsageLimitExceeded) && domain.ParseConversationUsageLimit(failure.Error(), nil) == nil {
		return ports.NewChatProviderFailure("Usage limit exceeded", failure.Error(), cause)
	}
	return failure
}

// threadStatusFrom maps the provider's thread status onto Tallpine's spelling. An
// unrecognized status yields the empty string, which the caller reads as "nothing
// to record" rather than guessing.
func threadStatusFrom(status codexproto.ThreadStatusType) domain.ThreadStatus {
	switch status {
	case codexproto.ThreadStatusTypeActive:
		return domain.ThreadStatusActive
	case codexproto.ThreadStatusTypeIdle:
		return domain.ThreadStatusIdle
	case codexproto.ThreadStatusTypeNotLoaded:
		return domain.ThreadStatusNotLoaded
	case codexproto.ThreadStatusTypeSystemError:
		return domain.ThreadStatusSystemError
	default:
		return ""
	}
}

// waitingOnFrom maps the provider's active flags onto Tallpine's spelling.
func waitingOnFrom(flags []codexproto.ThreadActiveFlag) []string {
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, 0, len(flags))
	for _, flag := range flags {
		switch flag {
		case codexproto.ThreadActiveFlagWaitingOnApproval:
			out = append(out, "waiting_on_approval")
		case codexproto.ThreadActiveFlagWaitingOnUserInput:
			out = append(out, "waiting_on_user_input")
		default:
			out = append(out, string(flag))
		}
	}
	return out
}
