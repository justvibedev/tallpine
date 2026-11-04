package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	mergessvc "tallpine.eu/tallpine/backend/internal/service/merges"
)

type fakeMerges struct{ project domain.ProjectID }

func (f *fakeMerges) Digest(_ context.Context, p domain.ProjectID) (mergessvc.Digest, error) {
	f.project = p
	return mergessvc.Digest{InFlight: []mergessvc.MergeEntry{{Number: 7, Lane: "B9"}}}, nil
}

func TestMergesRouteServesTheDigest(t *testing.T) {
	svc := &fakeMerges{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, log, nil, httpd.APIDeps{Merges: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	raw, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/projects/tp/merges", "")
	body := string(raw)
	if status != http.StatusOK || svc.project != "tp" || !strings.Contains(body, `"lane":"B9"`) {
		t.Fatalf("status=%d project=%q body=%s", status, svc.project, body)
	}
}
