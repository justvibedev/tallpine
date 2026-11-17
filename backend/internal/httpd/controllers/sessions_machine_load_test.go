package controllers_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/service/machineload"
)

type fakeMachineLoad struct {
	status machineload.Status
	err    error
}

func (f fakeMachineLoad) Check(context.Context) (machineload.Status, error) { return f.status, f.err }

func machineLoadServer(t *testing.T, gate fakeMachineLoad) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{Sessions: newFakeSessionService(), MachineLoad: gate}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func spawnWarnings(t *testing.T, srv *httptest.Server) []string {
	t.Helper()
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions", `{"projectId":"tp","kind":"worker","harness":"codex","prompt":"go"}`)
	var resp struct {
		Warnings []string `json:"warnings"`
	}
	if status != http.StatusCreated || json.Unmarshal(body, &resp) != nil {
		t.Fatalf("spawn = %d %s, want it to succeed", status, body)
	}
	return resp.Warnings
}

// A spawn is never held back by a busy machine; its response says so.
func TestSpawnWarnsWhenTheMachineIsBusy(t *testing.T) {
	status := machineload.Status{Busy: true, ByLoad: true, Load1: 9.5, Cores: 8, Reviewers: 1, MaxReviewers: 3, LoadPercent: 100}
	got := spawnWarnings(t, machineLoadServer(t, fakeMachineLoad{status: status}))
	if len(got) != 1 || got[0] != status.Reason() {
		t.Fatalf("warnings = %q, want [%q]", got, status.Reason())
	}
}

func TestSpawnAddsNoMachineWarningWhenQuietOrUnreadable(t *testing.T) {
	if got := spawnWarnings(t, machineLoadServer(t, fakeMachineLoad{})); len(got) != 0 {
		t.Fatalf("quiet machine warnings = %q", got)
	}
	busyButBroken := fakeMachineLoad{status: machineload.Status{Busy: true}, err: errors.New("no load reading")}
	if got := spawnWarnings(t, machineLoadServer(t, busyButBroken)); len(got) != 0 {
		t.Fatalf("a gate error added warnings %q; the spawn must succeed with none", got)
	}
}
