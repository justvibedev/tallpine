package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
)

// maxCleanupPolicyBody bounds a policy change; the whole policy is well under it.
const maxCleanupPolicyBody = 16 << 10

// CleanupService is the controller-facing automatic cleanup contract.
type CleanupService interface {
	Status(ctx context.Context) (cleanupsvc.Status, error)
	UpdatePolicy(ctx context.Context, patch []byte) (cleanupsvc.Policy, error)
	Run(ctx context.Context, req cleanupsvc.RunRequest) (cleanupsvc.Report, error)
	History(ctx context.Context, limit int, includeDryRuns bool) ([]cleanupsvc.Report, error)
}

// CleanupController owns the cleanup policy, manual runs and run history.
type CleanupController struct {
	// Audit records changes in the settings change log; nil records nothing.
	Audit *settingsaudit.Recorder
	Svc   CleanupService
}

// Register mounts the cleanup routes.
func (c *CleanupController) Register(r chi.Router) {
	r.Get("/cleanup", c.status)
	r.Patch("/cleanup/policy", c.updatePolicy)
	r.Post("/cleanup/runs", c.run)
	r.Get("/cleanup/runs", c.history)
}

func (c *CleanupController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/cleanup")
		return
	}
	status, err := c.Svc.Status(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := CleanupStatusResponse{
		Policy:           cleanupPolicyDTO(status.Policy),
		PolicyUpdatedAt:  formatOptionalTime(status.PolicyUpdated),
		Running:          status.Running,
		PolicyError:      status.PolicyError,
		FirstPassPending: status.FirstPassPending,
		LastFullRunAt:    formatOptionalTime(status.LastFullRunAt),
		NextLightRunAt:   formatOptionalTime(status.NextLightAt),
		NextFullRunAt:    formatOptionalTime(status.NextFullAt),
	}
	if status.LastRun != nil {
		report := cleanupReportDTO(*status.LastRun)
		out.LastRun = &report
	}
	if status.LastDryRun != nil {
		report := cleanupReportDTO(*status.LastDryRun)
		out.LastDryRun = &report
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *CleanupController) updatePolicy(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/cleanup/policy")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCleanupPolicyBody))
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_BODY", "could not read request body", nil)
		return
	}
	var before settingsaudit.Values
	if c.Audit != nil {
		if status, err := c.Svc.Status(r.Context()); err == nil {
			before = cleanupPolicyValues(status.Policy)
		}
	}
	policy, err := c.Svc.UpdatePolicy(r.Context(), body)
	if errors.Is(err, cleanupsvc.ErrInvalidPolicy) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CLEANUP_POLICY_INVALID", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if before != nil {
		c.Audit.Record(r.Context(), "", before, cleanupPolicyValues(policy))
	}
	envelope.WriteJSON(w, http.StatusOK, cleanupPolicyDTO(policy))
}

func (c *CleanupController) run(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/cleanup/runs")
		return
	}
	var req CleanupRunRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	pass := cleanupsvc.Pass(req.Pass)
	if pass == "" {
		pass = cleanupsvc.PassFull
	}
	if !pass.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CLEANUP_PASS_INVALID", "pass must be light or full", nil)
		return
	}
	only := make([]cleanupsvc.Category, 0, len(req.Only))
	for _, c := range req.Only {
		only = append(only, cleanupsvc.Category(c))
	}
	// A run outlives the request: once it started deleting, a closed client
	// must not leave it half done and unrecorded.
	report, err := c.Svc.Run(context.WithoutCancel(r.Context()), cleanupsvc.RunRequest{
		Pass: pass, DryRun: req.DryRun, Trigger: cleanupsvc.TriggerManual, ConfirmArtifacts: req.ConfirmArtifacts,
		Only: only,
	})
	if errors.Is(err, cleanupsvc.ErrBusy) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CLEANUP_IN_PROGRESS", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, cleanupReportDTO(report))
}

func (c *CleanupController) history(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/cleanup/runs")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	includeDry, _ := strconv.ParseBool(r.URL.Query().Get("includeDryRuns"))
	runs, err := c.Svc.History(r.Context(), limit, includeDry)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := CleanupRunsResponse{Runs: make([]CleanupRunReport, 0, len(runs))}
	for _, run := range runs {
		out.Runs = append(out.Runs, cleanupReportDTO(run))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func cleanupPolicyDTO(p cleanupsvc.Policy) CleanupPolicy {
	return CleanupPolicy{
		Automatic: p.Automatic,
		Worktrees: CleanupWorktreePolicy{Enabled: p.Worktrees.Enabled, RetentionDays: p.Worktrees.RetentionDays, RequireMerged: p.Worktrees.RequireMerged},
		Artifacts: CleanupArtifactsPolicy{
			Enabled: p.Artifacts.Enabled, RetentionDays: p.Artifacts.RetentionDays,
			AutoPrune: p.Artifacts.AutoPrune, DeliverableAction: string(p.Artifacts.DeliverableAction),
			AutoDeliverables: p.Artifacts.AutoDeliverables,
		},
		Branches: CleanupBranchesPolicy{
			Enabled: p.Branches.Enabled, MergedRetentionDays: p.Branches.MergedRetentionDays,
			StaleRetentionDays: p.Branches.StaleRetentionDays, DeleteRemote: p.Branches.DeleteRemote,
		},
		SessionTemp:   CleanupSessionTempPolicy{Enabled: p.SessionTemp.Enabled},
		HarnessCaches: CleanupHarnessCachesPolicy{Enabled: p.HarnessCaches.Enabled, RetentionDays: p.HarnessCaches.RetentionDays},
		SystemTemp: CleanupSystemTempPolicy{
			Enabled: p.SystemTemp.Enabled, MinAgeDays: p.SystemTemp.MinAgeDays,
			Patterns: append([]string{}, p.SystemTemp.Patterns...),
		},
		GoBuildCache: CleanupGoBuildCachePolicy{Enabled: p.GoBuildCache.Enabled},
		Containers:   CleanupContainersPolicy{Enabled: p.Containers.Enabled, VolumeRetentionDays: p.Containers.VolumeRetentionDays},
		Backlog:      CleanupBacklogPolicy{Enabled: p.Backlog.Enabled, InboxExpiryDays: p.Backlog.InboxExpiryDays},
	}
}

func cleanupReportDTO(r cleanupsvc.Report) CleanupRunReport {
	out := CleanupRunReport{
		ID: r.ID, StartedAt: r.StartedAt.UTC().Format(time.RFC3339), FinishedAt: r.FinishedAt.UTC().Format(time.RFC3339),
		Trigger: string(r.Trigger), Pass: string(r.Pass), DryRun: r.DryRun, FreedBytes: r.FreedBytes,
		Categories: make([]CleanupCategoryReport, 0, len(r.Categories)), Error: r.Error,
	}
	for _, cat := range r.Categories {
		dto := CleanupCategoryReport{
			Category: string(cat.Category), FreedBytes: cat.FreedBytes, WouldFreeBytes: cat.WouldFreeBytes,
			PendingBytes: cat.PendingBytes, Freed: cat.Freed, WouldFree: cat.WouldFree, Pending: cat.Pending,
			Kept: cat.Kept, Failed: cat.Failed, Items: make([]CleanupItem, 0, len(cat.Items)),
			ItemsTruncated: cat.ItemsTruncated, Skipped: cat.Skipped, Error: cat.Error,
		}
		for _, item := range cat.Items {
			dto.Items = append(dto.Items, CleanupItem{
				SessionID: string(item.SessionID), ProjectID: string(item.ProjectID), What: item.What,
				Path: item.Path, Bytes: item.Bytes, Status: string(item.Status), Reason: item.Reason,
			})
		}
		out.Categories = append(out.Categories, dto)
	}
	return out
}
