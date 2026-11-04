package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

// configOptions serves every live control the provider advertises. Empty is a
// real capability answer and keeps native drivers free to use Tallpine's older typed
// model/settings surface.
func (c *ConversationsController) configOptions(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation/config-options")
		return
	}
	options, err := c.Svc.ConfigOptions(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		if errors.Is(err, chatsvc.ErrConfigOptionsUnsupported) {
			envelope.WriteJSON(w, http.StatusOK, ConversationConfigOptionsResponse{
				Options: []ConversationConfigOptionResponse{},
			})
			return
		}
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, configOptionsPayload(options))
}

// setConfigOption applies one advertised provider value and returns the complete
// rebuilt catalog. This is immediate session state, not a preference to infer on
// the next turn: changing models can alter the other available controls now.
func (c *ConversationsController) setConfigOption(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/conversation/config-options/{configId}")
		return
	}
	var req SetConversationConfigOptionRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if (req.Value == "") == (req.Enabled == nil) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_CONFIG_OPTION_VALUE_REQUIRED",
			"provide exactly one of value or enabled", nil)
		return
	}
	options, err := c.Svc.SetConfigOption(
		r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")),
		chi.URLParam(r, "configId"),
		ports.ChatConfigOptionValue{Select: req.Value, Boolean: req.Enabled},
	)
	if writeModelPolicyError(w, r, err) {
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, configOptionsPayload(options))
}

// reloadMCPServers restarts the provider's tool servers for a session.
//
// A write, not a read, and the only recovery there is for the failure it addresses:
// a tool server that failed to start stays failed for the life of the provider
// process, so without this a session loses a tool permanently and the alternative is
// throwing the conversation away.
func (c *ConversationsController) reloadMCPServers(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/mcp/reload")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	servers, err := c.Svc.ReloadMCPServers(r.Context(), session)
	if errors.Is(err, chatsvc.ErrTurnRunning) {
		// Answered here rather than in writeConversationError, whose CHAT_TURN_RUNNING
		// message names rolling back. Same code and same retryable meaning; a reader
		// of this one is not undoing anything.
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_RUNNING",
			"stop the agent before reloading its tool servers: it is in the middle of a turn", nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReloadConversationMCPServersResponse{
		Servers: mcpServersPayload(servers),
	})
}

// setTitle names the provider's thread.
//
// 202 rather than 200: the provider accepts the name and then reports it back on its
// own event, and that report is what moves Tallpine's session label. Claiming 200 would
// promise a change that has not happened yet.
func (c *ConversationsController) setTitle(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/sessions/{sessionId}/conversation/title")
		return
	}
	var req SetConversationTitleRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	title, err := c.Svc.SetTitle(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), req.Title)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, SetConversationTitleResponse{Title: title})
}

// chatPrewarmer runs an agent's Chat readiness check ahead of need.
type chatPrewarmer interface {
	PrewarmChat(harness domain.AgentHarness)
}

// prewarmAgent starts an agent's Chat readiness check in the background when it
// is picked, so the switch the next send makes does not wait on it.
func (c *ConversationsController) prewarmAgent(w http.ResponseWriter, r *http.Request) {
	prewarmer, ok := c.Svc.(chatPrewarmer)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/agents/{agent}/chat/prewarm")
		return
	}
	agent := strings.TrimSpace(chi.URLParam(r, "agent"))
	prewarmer.PrewarmChat(domain.AgentHarness(agent))
	envelope.WriteJSON(w, http.StatusAccepted, PrewarmChatAgentResponse{Agent: agent})
}

// chatAvailability reports agents whose chat adapter is missing.
type chatAvailability interface {
	ChatUnavailable(ctx context.Context, harnesses []domain.AgentHarness) map[domain.AgentHarness]string
}

// unavailableChatAgents lists the asked agents missing a chat adapter. It starts nothing.
func (c *ConversationsController) unavailableChatAgents(w http.ResponseWriter, r *http.Request) {
	checker, ok := c.Svc.(chatAvailability)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/chat/unavailable")
		return
	}
	harnesses := queryHarnesses(r)
	unavailable := checker.ChatUnavailable(r.Context(), harnesses)
	response := UnavailableChatAgentsResponse{Agents: []UnavailableChatAgent{}}
	for _, harness := range harnesses {
		if reason, missing := unavailable[harness]; missing {
			response.Agents = append(response.Agents, UnavailableChatAgent{Agent: string(harness), Reason: reason})
		}
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

// queryHarnesses reads the comma-separated agents query, at most 64 of them.
func queryHarnesses(r *http.Request) []domain.AgentHarness {
	var harnesses []domain.AgentHarness
	for _, agent := range strings.Split(r.URL.Query().Get("agents"), ",") {
		if agent = strings.TrimSpace(agent); agent != "" && len(harnesses) < 64 {
			harnesses = append(harnesses, domain.AgentHarness(agent))
		}
	}
	return harnesses
}

// chatPermissionModesLister reports the permission modes a harness runs a chat in.
type chatPermissionModesLister interface {
	PermissionModes(ctx context.Context, harness domain.AgentHarness) ([]ports.PermissionMode, error)
}

// chatPermissionModes lists the modes each asked agent runs a chat in, so a
// picker can show the one a switch starts it in. Agents with no chat are left out.
func (c *ConversationsController) chatPermissionModes(w http.ResponseWriter, r *http.Request) {
	lister, ok := c.Svc.(chatPermissionModesLister)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/agents/chat/permission-modes")
		return
	}
	response := ChatPermissionModesResponse{Agents: []ChatAgentPermissionModes{}}
	for _, harness := range queryHarnesses(r) {
		modes, err := lister.PermissionModes(r.Context(), harness)
		if err != nil || len(modes) == 0 {
			continue
		}
		entry := ChatAgentPermissionModes{Agent: string(harness), Modes: make([]string, 0, len(modes))}
		for _, mode := range modes {
			entry.Modes = append(entry.Modes, string(mode))
		}
		response.Agents = append(response.Agents, entry)
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

// models serves the provider's catalog for this session plus the current choice.
func (c *ConversationsController) models(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation/models")
		return
	}
	session := domain.SessionID(chi.URLParam(r, "sessionId"))
	models, selected, err := c.Svc.Models(r.Context(), session)
	if err != nil {
		if errors.Is(err, chatsvc.ErrModelsUnsupported) {
			// Not a failure: this agent simply offers no choice. An empty list with
			// the current selection lets the client hide the picker rather than
			// show an error the user cannot act on.
			envelope.WriteJSON(w, http.StatusOK, ConversationModelsResponse{
				Models:   []ConversationModelResponse{},
				Selected: turnSettingsPayload(selected),
			})
			return
		}
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationModelsResponse(models, selected))
}

// setSettings records the provider choices for the next turn.
func (c *ConversationsController) setSettings(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}/conversation/settings")
		return
	}
	var req ConversationTurnSettingsPayload
	if !decodeConversationBody(w, r, &req) {
		return
	}
	// An unknown approval mode is refused rather than normalized: silently
	// downgrading a permission choice is the one direction that must never happen
	// by accident.
	approval := domain.PermissionMode(req.ApprovalMode)
	if req.ApprovalMode != "" && !approval.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_APPROVAL_MODE_INVALID",
			fmt.Sprintf("unknown approval mode %q", req.ApprovalMode), nil)
		return
	}

	if !domain.ValidAutoCompactWindow(req.AutoCompactWindow) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_COMPACT_WINDOW_INVALID",
			fmt.Sprintf("unknown auto-compact window %q", req.AutoCompactWindow), nil)
		return
	}
	settings, err := c.Svc.SetTurnSettings(r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")), domain.ConversationSettings{
			Model:           req.Model,
			ReasoningEffort: req.ReasoningEffort,
			ApprovalMode:    approval,

			FastMode:          req.FastMode,
			PlanMode:          req.PlanMode,
			AutoCompactWindow: req.AutoCompactWindow,
		})
	if writeModelPolicyError(w, r, err) {
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, turnSettingsPayload(settings))
}

func conversationModelsResponse(
	models []ports.ChatModel,
	selected domain.ConversationSettings,
) ConversationModelsResponse {
	out := ConversationModelsResponse{
		Models:   make([]ConversationModelResponse, 0, len(models)),
		Selected: turnSettingsPayload(selected),
	}
	for _, model := range models {
		out.Models = append(out.Models, ConversationModelResponse{
			ID:            model.ID,
			DisplayName:   model.DisplayName,
			Description:   model.Description,
			Default:       model.Default,
			Efforts:       model.Efforts,
			DefaultEffort: model.DefaultEffort,
			SupportsFast:  model.FastTier != "",
		})
	}
	return out
}

func configOptionsPayload(options []ports.ChatConfigOption) ConversationConfigOptionsResponse {
	out := ConversationConfigOptionsResponse{
		Options: make([]ConversationConfigOptionResponse, 0, len(options)),
	}
	for _, option := range options {
		item := ConversationConfigOptionResponse{
			ID:             option.ID,
			Name:           option.Name,
			Description:    option.Description,
			Category:       option.Category,
			Type:           string(option.Type),
			CurrentValue:   option.Current.Select,
			CurrentBoolean: option.Current.Boolean,
			Choices:        make([]ConversationConfigChoiceResponse, 0, len(option.Choices)),
		}
		for _, choice := range option.Choices {
			item.Choices = append(item.Choices, ConversationConfigChoiceResponse{
				PermissionMode: choice.PermissionMode,
				Value:          choice.Value,
				Name:           choice.Name,
				Description:    choice.Description,
				Group:          choice.Group,
				GroupName:      choice.GroupName,
			})
		}
		out.Options = append(out.Options, item)
	}
	return out
}

func turnSettingsPayload(settings domain.ConversationSettings) ConversationTurnSettingsPayload {
	return ConversationTurnSettingsPayload{
		Model:           settings.Model,
		ReasoningEffort: settings.ReasoningEffort,
		ApprovalMode:    string(settings.ApprovalMode),

		FastMode:                 settings.FastMode,
		PlanMode:                 settings.PlanMode,
		AutoCompactWindow:        settings.AutoCompactWindow,
		AutoCompactPending:       settings.AutoCompactPending,
		AutoCompactProjectTokens: settings.AutoCompactProjectTokens,
		ContextCapacity:          settings.ContextCapacity,
		CompactThreshold:         settings.CompactThreshold,
		ModelLabel:               settings.ModelLabel,
	}
}

// modelUsageReader is the optional Chat service ability to report a session's
// cumulative token spend by model.
type modelUsageReader interface {
	ModelUsage(ctx context.Context, session domain.SessionID) ([]domain.ModelUsage, error)
}

func (c *ConversationsController) modelUsage(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/usage")
		return
	}
	out := ModelUsageResponse{Models: []ModelUsagePayload{}}
	if reader, ok := c.Svc.(modelUsageReader); ok {
		session := domain.SessionID(chi.URLParam(r, "sessionId"))
		rows, err := reader.ModelUsage(r.Context(), session)
		if errors.Is(err, chatsvc.ErrNotChatMode) && c.TerminalUsage != nil {
			var supported bool
			if rows, supported, err = c.TerminalUsage.TerminalModelUsage(r.Context(), session); supported && err == nil {
				for _, u := range rows {
					out.Models = append(out.Models, ModelUsagePayload(u))
				}
				envelope.WriteJSON(w, http.StatusOK, out)
				return
			}
			if err == nil {
				err = chatsvc.ErrNotChatMode
			}
		}
		if err != nil {
			writeConversationError(w, r, err)
			return
		}
		for _, u := range rows {
			out.Models = append(out.Models, ModelUsagePayload(u))
		}
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

// contextBreakdownReader is the optional Chat service ability to report what
// fills a running agent's context window.
type contextBreakdownReader interface {
	ContextBreakdown(ctx context.Context, session domain.SessionID) (domain.ContextBreakdown, bool, error)
}

// contextBreakdown answers on request only: the agent may count each category
// with a model API, so it is never part of the conversation snapshot.
func (c *ConversationsController) contextBreakdown(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/conversation/context-breakdown")
		return
	}
	reader, ok := c.Svc.(contextBreakdownReader)
	if !ok {
		envelope.WriteJSON(w, http.StatusOK, ContextBreakdownResponse{})
		return
	}
	breakdown, available, err := reader.ContextBreakdown(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")))
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, contextBreakdownPayload(breakdown, available))
}

func contextBreakdownPayload(b domain.ContextBreakdown, available bool) ContextBreakdownResponse {
	if !available {
		return ContextBreakdownResponse{}
	}
	out := ContextBreakdownResponse{
		Available: true, Model: b.Model, TotalTokens: b.TotalTokens, MaxTokens: b.MaxTokens,
		AutoCompactThreshold: b.AutoCompactThreshold,
		Categories:           make([]ContextCategoryPayload, 0, len(b.Categories)),
	}
	for _, category := range b.Categories {
		out.Categories = append(out.Categories, ContextCategoryPayload{ID: category.ID, Name: category.Name, Tokens: category.Tokens, Kind: string(category.Kind)})
	}
	if b.Messages != nil {
		parts := ContextMessagePartsPayload(*b.Messages)
		out.Messages = &parts
	}
	for _, file := range b.MemoryFiles {
		out.MemoryFiles = append(out.MemoryFiles, ContextMemoryFilePayload(file))
	}
	return out
}
