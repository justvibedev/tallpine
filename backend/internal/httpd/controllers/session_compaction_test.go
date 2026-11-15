package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
)

func compactionServer(t *testing.T, view domain.AutoCompactView) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: newFakeSessionService(), Compaction: func(context.Context, domain.Session) (domain.AutoCompactView, bool) {
		return view, true
	}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

// The session detail carries the compaction readout (B735).
func TestSessionDetailCarriesCompaction(t *testing.T) {
	srv := compactionServer(t, domain.AutoCompactView{Baseline: 51_000, Window: 151_000, Trigger: 118_000, ModelWindow: 1_000_000})
	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1", "")
	var resp struct {
		Compaction *domain.AutoCompactView `json:"compaction"`
	}
	if status != http.StatusOK || json.Unmarshal(body, &resp) != nil || resp.Compaction == nil ||
		resp.Compaction.Baseline != 51_000 || resp.Compaction.Window != 151_000 || resp.Compaction.Trigger != 118_000 {
		t.Fatalf("detail = %d %s", status, body)
	}
}

// A spawn whose baseline leaves too little room on the model still starts,
// with a warning and the readout.
func TestSpawnWarnsWhenTheBaselineLeavesLittleRoom(t *testing.T) {
	srv := compactionServer(t, domain.AutoCompactView{Baseline: 150_000, ModelWindow: 200_000, RoomTight: true})
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", `{"projectId":"tp","kind":"worker","harness":"codex","prompt":"go"}`)
	var resp struct {
		Warnings   []string                `json:"warnings"`
		Compaction *domain.AutoCompactView `json:"compaction"`
	}
	if status != http.StatusCreated || json.Unmarshal(body, &resp) != nil || resp.Compaction == nil || !resp.Compaction.RoomTight {
		t.Fatalf("spawn = %d %s", status, body)
	}
	want := (domain.AutoCompactView{Baseline: 150_000, ModelWindow: 200_000, RoomTight: true}).RoomWarning()
	if len(resp.Warnings) != 1 || resp.Warnings[0] != want {
		t.Fatalf("warnings = %q, want [%q]", resp.Warnings, want)
	}
	srv = compactionServer(t, domain.AutoCompactView{Baseline: 66_000, Window: 166_000, Trigger: 133_000, ModelWindow: 1_000_000})
	body, status, _ = doRequest(t, srv, http.MethodPost, "/api/v1/sessions", `{"projectId":"tp","kind":"worker","harness":"codex","prompt":"go"}`)
	resp.Warnings = nil
	if status != http.StatusCreated || json.Unmarshal(body, &resp) != nil || len(resp.Warnings) != 0 {
		t.Fatalf("roomy spawn = %d %s", status, body)
	}
}
