package controllers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
)

// ReportService is the controller-facing report creation contract.
type ReportService interface {
	Create(context.Context, reportsvc.CreateInput) (domain.ReportRecord, error)
}

type reportProjectReader interface {
	ListProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error)
}

// RouteNoticer says where reports go when not to the project's orchestrator.
type RouteNoticer interface {
	RouteNotice(ctx context.Context, project domain.ProjectID, worker, parent domain.SessionID) string
}

// ReportsController owns the /reports routes.
type ReportsController struct{ Svc ReportService }

// Register mounts report routes on r.
func (c *ReportsController) Register(r chi.Router) {
	r.Post("/reports", c.create)
	r.Get("/reports", c.list)
}

func (c *ReportsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/reports")
		return
	}
	reader, ok := c.Svc.(reportProjectReader)
	if !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/reports")
		return
	}
	projectID := domain.ProjectID(r.URL.Query().Get("projectId"))
	if agent, ok := scopedAgent(r); ok && projectID != "" && !agent.MayActOn(projectID) {
		writeAgentScopeForbidden(w, r, "read another project's reports")
		return
	}
	if projectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "REPORT_PROJECT_REQUIRED", "Project id is required", nil)
		return
	}
	records, err := reader.ListProject(r.Context(), projectID)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	response := ListReportsResponse{Reports: make([]ReportResponse, len(records))}
	for i, record := range records {
		outputs := make([]ReportOutputResponse, len(record.Outputs))
		for j, output := range record.Outputs {
			outputs[j] = ReportOutputResponse{Kind: string(output.Kind), Reference: output.Reference, Label: output.Label}
		}
		response.Reports[i] = ReportResponse{
			ID: record.ID, SessionID: string(record.SessionID), ProjectID: string(record.ProjectID),
			State: string(record.State), Note: record.Note, Message: record.Message,
			Outputs: outputs, CreatedAt: record.CreatedAt, RepeatCount: record.RepeatCount,
			Undelivered: record.UndeliveredReason(),
		}
	}
	envelope.WriteJSON(w, http.StatusOK, response)
}

func (c *ReportsController) create(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/reports")
		return
	}
	var req CreateReportRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if agent, ok := scopedAgent(r); ok {
		switch domain.SessionID(req.SessionID) {
		case "":
			req.SessionID = string(agent.SessionID)
		case agent.SessionID:
		default:
			writeAgentScopeForbidden(w, r, "report for another session")
			return
		}
	}
	outputs := make([]domain.ReportOutput, len(req.Outputs))
	for i, output := range req.Outputs {
		outputs[i] = domain.ReportOutput{Kind: domain.ReportOutputKind(output.Kind), Reference: output.Reference, Label: output.Label}
	}
	rec, err := c.Svc.Create(r.Context(), reportsvc.CreateInput{
		SessionID: domain.SessionID(req.SessionID), State: domain.ReportState(req.State),
		Note: req.Note, Message: req.Message, Outputs: outputs, Defer: req.Defer,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := CreateReportResponse{ID: rec.ID}
	for _, d := range rec.Deferred {
		resp.Deferred = append(resp.Deferred, DeferOutcomeResponse{Line: d.Line, Status: string(d.Status), Ref: d.Ref, Reason: d.Reason})
	}
	if rn, ok := c.Svc.(RouteNoticer); ok {
		resp.Notice = rn.RouteNotice(r.Context(), rec.ProjectID, rec.SessionID, "")
	}
	envelope.WriteJSON(w, http.StatusCreated, resp)
}
