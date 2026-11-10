package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
	healthsvc "tallpine.eu/tallpine/backend/internal/service/health"
)

// HealthService is the controller-facing silent-failure summary.
type HealthService interface {
	Status(ctx context.Context) (healthsvc.Status, error)
	StatusForProject(ctx context.Context, project domain.ProjectID) (healthsvc.Status, error)
}

// HealthController serves the silent-failure summary `tallpine doctor` and the
// desktop health badge both read.
type HealthController struct {
	Svc HealthService
	// PeerCheck adds the peer-check report for full-authority callers.
	PeerCheck *peeradmit.Checker
}

// Register mounts the health route.
func (c *HealthController) Register(r chi.Router) {
	r.Get("/health/silent-failures", c.status)
}

func (c *HealthController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/health/silent-failures")
		return
	}
	var st healthsvc.Status
	var err error
	agent, scoped := scopedAgent(r)
	if scoped {
		// Scoped tokens (orchestrators only, per agentRules) see their own
		// project's issues, never the whole daemon's.
		st, err = c.Svc.StatusForProject(r.Context(), agent.ProjectID)
	} else {
		st, err = c.Svc.Status(r.Context())
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := SilentFailuresResponse{
		Healthy:         st.Healthy(),
		ChatNotAttached: make([]SilentFailuresChatIssue, 0, len(st.ChatNotAttached)),
		ReportBatches:   make([]SilentFailuresReportIssue, 0, len(st.ReportBatches)),
		TurnFallbacks:   make([]SilentFailuresFallbackIssue, 0, len(st.TurnFallbacks)),
	}
	for _, c := range st.ChatNotAttached {
		out.ChatNotAttached = append(out.ChatNotAttached, SilentFailuresChatIssue{SessionID: string(c.SessionID)})
	}
	for _, b := range st.ReportBatches {
		out.ReportBatches = append(out.ReportBatches, SilentFailuresReportIssue{
			ProjectID: string(b.ProjectID), Pending: b.Pending, Claimed: b.Claimed,
			Undeliverable: b.Undeliverable, Since: formatOptionalTime(b.Since),
		})
	}
	for _, f := range st.TurnFallbacks {
		out.TurnFallbacks = append(out.TurnFallbacks, SilentFailuresFallbackIssue{ID: f.ID, SessionID: string(f.SessionID), DueAt: formatOptionalTime(f.DueAt)})
	}
	if c.PeerCheck != nil && !scoped {
		out.PeerCheck = peerCheckDTO(c.PeerCheck.Report())
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func peerCheckDTO(report peeradmit.Report) *SilentFailuresPeerCheck {
	dto := &SilentFailuresPeerCheck{
		Mode: string(report.Mode), ModeReason: report.ModeReason, TeamID: report.TeamID,
		Admitted:    make(map[string]int64, len(report.Admitted)),
		WouldRefuse: make([]SilentFailuresPeerRefusal, 0, len(report.WouldRefuse)),
		Overflow:    report.Overflow, Dropped: report.Dropped,
	}
	for class, count := range report.Admitted {
		dto.Admitted[string(class)] = count
	}
	for _, rec := range report.WouldRefuse {
		dto.WouldRefuse = append(dto.WouldRefuse, SilentFailuresPeerRefusal{
			Executable: rec.Executable, TokenClass: rec.TokenClass, Route: rec.Route,
			Reason: string(rec.Reason), Count: rec.Count, LastPID: rec.LastPID, LastSeen: formatOptionalTime(rec.LastSeen),
		})
	}
	return dto
}
