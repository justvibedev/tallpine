package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
)

// SettingsService is the controller-facing preferences contract. It is declared
// here, at the consumer, on purpose (Go idiom): settingswrite declares its own
// narrower write-side interfaces, and the overlap is not a dedupe target.
type SettingsService interface {
	Get(ctx context.Context) (settingssvc.Snapshot, error)
	SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) (settingssvc.Snapshot, error)
	SetHarnessDefault(ctx context.Context, agent domain.AgentHarness, value domain.HarnessDefault) (settingssvc.Snapshot, error)
	SetChatHibernation(ctx context.Context, update settingssvc.ChatHibernationUpdate) (settingssvc.Snapshot, error)
	SetVersionRefresh(ctx context.Context, enabled bool) (settingssvc.Snapshot, error)
	SetStorageCompaction(ctx context.Context, enabled *bool, retentionDays *int) (settingssvc.Snapshot, error)
	ChatHarnesses(candidates []domain.AgentHarness) []domain.AgentHarness
	Offering() settingssvc.Offering
}

// SettingsController owns the daemon-owned preference routes.
//
// These are daemon-owned rather than renderer-owned on purpose: desktop, mobile,
// and the CLI all resolve the same value, so a preference held in one client would
// disagree with the others.
type SettingsController struct {
	// Audit records changes in the settings change log; nil records nothing.
	Audit *settingsaudit.Recorder
	Svc   SettingsService
	// Compaction, when set, is told after a harness's compaction default is
	// saved so running chats can follow it.
	Compaction CompactionDefaultApplier
}

// CompactionDefaultApplier moves running chats of a harness to its new
// compaction default where the agent allows it.
type CompactionDefaultApplier interface {
	ApplyCompactionDefault(ctx context.Context, harness domain.AgentHarness)
}

// Register mounts the settings routes.
func (c *SettingsController) Register(r chi.Router) {
	r.Get("/settings", c.get)
	r.Patch("/settings/session-interface", c.setSessionInterface)
	r.Patch("/settings/chat-hibernation", c.setChatHibernation)
	r.Patch("/settings/version-refresh", c.setVersionRefresh)
	r.Patch("/settings/dynamic-orchestrator-context", c.setDynamicOrchestratorContext)
	r.Patch("/settings/workflow-defaults", c.setWorkflowDefaults)
	r.Patch("/settings/storage-compaction", c.setStorageCompaction)
	r.Patch("/settings/harness-defaults/{agent}", c.setHarnessDefault)
}

func (c *SettingsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings")
		return
	}
	snapshot, err := c.Svc.Get(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}

func (c *SettingsController) setSessionInterface(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/session-interface")
		return
	}
	var req UpdateSessionInterfaceRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}

	// Parsed strictly: an unrecognized value is rejected rather than collapsing to
	// a default the caller did not ask for.
	mode, err := domain.ParseSessionMode(req.DefaultSessionMode)
	if err != nil || mode == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"SESSION_MODE_INVALID", `defaultSessionMode must be "chat" or "tui"`, nil)
		return
	}

	prev, prevOK := c.appSnapshotForAudit(r.Context())
	snapshot, err := c.Svc.SetDefaultSessionMode(r.Context(), mode)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.recordApp(r.Context(), prev, prevOK, snapshot)
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}

func (c *SettingsController) setChatHibernation(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/chat-hibernation")
		return
	}
	var req UpdateChatHibernationRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.Enabled == nil && req.WorkerIdleSeconds == nil && req.OrchestratorIdleSeconds == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_HIBERNATION_INVALID", "set enabled, workerIdleSeconds or orchestratorIdleSeconds", nil)
		return
	}
	// Bound the seconds before converting: a huge value would overflow the
	// Duration and slip past the service's range check.
	maxSeconds := int64(domain.MaxChatHibernationIdle / time.Second)
	for _, seconds := range []*int64{req.WorkerIdleSeconds, req.OrchestratorIdleSeconds} {
		if seconds != nil && (*seconds < 0 || *seconds > maxSeconds) {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
				"CHAT_HIBERNATION_INVALID", fmt.Sprintf("idle thresholds must be between 60 and %d seconds", maxSeconds), nil)
			return
		}
	}
	update := settingssvc.ChatHibernationUpdate{Enabled: req.Enabled}
	if req.WorkerIdleSeconds != nil {
		d := time.Duration(*req.WorkerIdleSeconds) * time.Second
		update.WorkerIdle = &d
	}
	if req.OrchestratorIdleSeconds != nil {
		d := time.Duration(*req.OrchestratorIdleSeconds) * time.Second
		update.OrchestratorIdle = &d
	}
	prev, prevOK := c.appSnapshotForAudit(r.Context())
	snapshot, err := c.Svc.SetChatHibernation(r.Context(), update)
	if errors.Is(err, settingssvc.ErrInvalidChatHibernation) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_HIBERNATION_INVALID", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.recordApp(r.Context(), prev, prevOK, snapshot)
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}

func (c *SettingsController) setVersionRefresh(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/version-refresh")
		return
	}
	var req UpdateVersionRefreshRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"VERSION_REFRESH_INVALID", "enabled must be true or false", nil)
		return
	}
	prev, prevOK := c.appSnapshotForAudit(r.Context())
	snapshot, err := c.Svc.SetVersionRefresh(r.Context(), *req.Enabled)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.recordApp(r.Context(), prev, prevOK, snapshot)
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}

// dynamicContextSetter is the optional settings ability to change the global
// dynamic orchestrator context toggle (B413).
type dynamicContextSetter interface {
	SetDynamicOrchestratorContext(ctx context.Context, enabled bool) (settingssvc.Snapshot, error)
}

func (c *SettingsController) setDynamicOrchestratorContext(w http.ResponseWriter, r *http.Request) {
	setter, ok := c.Svc.(dynamicContextSetter)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/dynamic-orchestrator-context")
		return
	}
	var req UpdateDynamicOrchestratorContextRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.Enabled == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"DYNAMIC_ORCHESTRATOR_CONTEXT_INVALID", "enabled must be true or false", nil)
		return
	}
	prev, prevOK := c.appSnapshotForAudit(r.Context())
	snapshot, err := setter.SetDynamicOrchestratorContext(r.Context(), *req.Enabled)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.recordApp(r.Context(), prev, prevOK, snapshot)
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}

// workflowDefaultsSetter is the optional settings ability to change the app-wide
// workflow defaults (B525).
type workflowDefaultsSetter interface {
	SetWorkflowDefaults(ctx context.Context, update settingssvc.WorkflowDefaultsUpdate) (settingssvc.Snapshot, error)
}

func (c *SettingsController) setWorkflowDefaults(w http.ResponseWriter, r *http.Request) {
	setter, ok := c.Svc.(workflowDefaultsSetter)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/workflow-defaults")
		return
	}
	var req UpdateWorkflowDefaultsRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	update := settingssvc.WorkflowDefaultsUpdate{AutoReview: req.AutoReview, PlanAutoApprove: req.PlanAutoApprove}
	if req.PlanningMode != nil {
		mode := domain.PlanningMode(*req.PlanningMode)
		if mode == "" || !mode.Valid() {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
				"WORKFLOW_DEFAULTS_INVALID", "planningMode must be auto, always or off", nil)
			return
		}
		update.PlanningMode = &mode
	}
	if req.ReviewerHarness != nil {
		harness := domain.ReviewerHarness(*req.ReviewerHarness)
		if harness != "" && !harness.IsKnown() {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
				"WORKFLOW_DEFAULTS_INVALID", "reviewerHarness must be a supported reviewer or empty", nil)
			return
		}
		update.ReviewerHarness = &harness
	}
	update.ReviewerModel = req.ReviewerModel
	if req.AutoReview == nil && req.PlanningMode == nil && req.PlanAutoApprove == nil && req.ReviewerHarness == nil && req.ReviewerModel == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"WORKFLOW_DEFAULTS_INVALID", "provide autoReview, planningMode, planAutoApprove, reviewerHarness or reviewerModel", nil)
		return
	}
	prev, prevOK := c.appSnapshotForAudit(r.Context())
	snapshot, err := setter.SetWorkflowDefaults(r.Context(), update)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.recordApp(r.Context(), prev, prevOK, snapshot)
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}

func (c *SettingsController) setStorageCompaction(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/storage-compaction")
		return
	}
	var req UpdateStorageCompactionRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	prev, prevOK := c.appSnapshotForAudit(r.Context())
	snapshot, err := c.Svc.SetStorageCompaction(r.Context(), req.Enabled, req.ProviderEventRetentionDays)
	if errors.Is(err, settingssvc.ErrInvalidStorageCompaction) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"STORAGE_COMPACTION_INVALID", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.recordApp(r.Context(), prev, prevOK, snapshot)
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}

func (c *SettingsController) response(snapshot settingssvc.Snapshot) SettingsResponse {
	// Reported so the client can warn that choosing chat narrows which agents are
	// available, instead of letting the user discover it at spawn time.
	chatHarnesses := c.Svc.ChatHarnesses(domain.AllHarnesses)
	names := make([]string, 0, len(chatHarnesses))
	for _, harness := range chatHarnesses {
		names = append(names, string(harness))
	}
	offering := c.Svc.Offering()
	return SettingsResponse{
		DefaultSessionMode:                     string(snapshot.DefaultSessionMode),
		HarnessDefaults:                        snapshot.HarnessDefaults,
		ChatHarnesses:                          names,
		Client:                                 offering.Client,
		LocalEnabled:                           offering.LocalEnabled,
		ChatHibernationEnabled:                 snapshot.ChatHibernation.Enabled,
		ChatHibernationWorkerIdleSeconds:       int64(snapshot.ChatHibernation.WorkerIdle / time.Second),
		ChatHibernationOrchestratorIdleSeconds: int64(snapshot.ChatHibernation.OrchestratorIdle / time.Second),
		VersionRefresh:                         snapshot.VersionRefresh,
		WorkflowDefaults:                       WorkflowDefaultsResponse{AutoReview: snapshot.WorkflowDefaults.AutoReview, PlanningMode: string(snapshot.WorkflowDefaults.PlanningMode), PlanAutoApprove: snapshot.WorkflowDefaults.PlanAutoApprove, ReviewerHarness: string(snapshot.WorkflowDefaults.ReviewerHarness), ReviewerModel: snapshot.WorkflowDefaults.ReviewerModel},
		DynamicOrchestratorContext:             snapshot.DynamicOrchestratorContext,
		StorageCompaction:                      snapshot.StorageCompaction,
		ProviderEventRetentionDays:             snapshot.ProviderEventRetentionDays,
		TrackerIntakeEnabled:                   offering.TrackerIntakeEnabled,
		BacklogRunEnabled:                      offering.BacklogRunEnabled,
	}
}

func (c *SettingsController) setHarnessDefault(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/settings/harness-defaults/{agent}")
		return
	}
	var req UpdateHarnessDefaultRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.DisabledModels == nil && req.AutoCompactWindow == nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "HARNESS_DEFAULT_INVALID", "send disabledModels (an empty list offers every model) or autoCompactWindow", nil)
		return
	}
	agent := domain.AgentHarness(chi.URLParam(r, "agent"))
	// A field left out keeps its saved value, so the models panel and the
	// compaction default can save independently.
	current, err := c.Svc.Get(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	next := current.HarnessDefaults[string(agent)]
	if req.DisabledModels != nil {
		next.DisabledModels = *req.DisabledModels
	}
	if req.AutoCompactWindow != nil {
		next.AutoCompactWindow = *req.AutoCompactWindow
	}
	prev, prevOK := c.appSnapshotForAudit(r.Context())
	snapshot, err := c.Svc.SetHarnessDefault(r.Context(), agent, next)
	if errors.Is(err, settingssvc.ErrUnknownHarness) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "HARNESS_NOT_FOUND", err.Error(), nil)
		return
	}
	if errors.Is(err, settingssvc.ErrInvalidHarnessDefault) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "HARNESS_DEFAULT_INVALID", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if req.AutoCompactWindow != nil && c.Compaction != nil {
		// The default is saved; chats already running follow it where they can.
		c.Compaction.ApplyCompactionDefault(r.Context(), agent)
	}
	c.recordApp(r.Context(), prev, prevOK, snapshot)
	envelope.WriteJSON(w, http.StatusOK, c.response(snapshot))
}
