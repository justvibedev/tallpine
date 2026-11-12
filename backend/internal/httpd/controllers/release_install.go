package controllers

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/releaseinstall"
)

// ReleaseInstaller verifies a local release build and starts its detached
// install (releaseinstall.Installer).
type ReleaseInstaller interface {
	Install(ctx context.Context, sha string) (releaseinstall.Result, error)
}

// ReleaseInstallController serves the orchestrator-only release install: the
// orchestrator's sandbox cannot run scripts/release-local, so the daemon does.
type ReleaseInstallController struct {
	Svc   ReleaseInstaller
	Build ReleaseBuildRequester
}

// ReleaseBuildRequester starts the daemon-run `release-local request-build` for
// a project (releaseinstall.BuildRequester).
type ReleaseBuildRequester interface {
	Request(ctx context.Context, projectID string) (releaseinstall.Result, error)
}

// ReleaseBuildResponse reports the started build request.
type ReleaseBuildResponse struct {
	Log string `json:"log"`
	PID int    `json:"pid"`
}

// Register mounts the release routes.
func (c *ReleaseInstallController) Register(r chi.Router) {
	r.Post("/release/install", c.install)
	r.Post("/release/request-build", c.requestBuild)
}

// ReleaseInstallRequest names the build under release-builds to install.
type ReleaseInstallRequest struct {
	SHA string `json:"sha"`
}

// ReleaseInstallResponse reports the started install.
type ReleaseInstallResponse struct {
	SHA string `json:"sha"`
	App string `json:"app"`
	Log string `json:"log"`
	// Result is where the install writes its outcome when it ends: line 1
	// "ok" or "failed", line 2 the message. Poll it; nothing is sent.
	Result string `json:"result,omitempty"`
	PID    int    `json:"pid"`
}

func (c *ReleaseInstallController) install(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release/install")
		return
	}
	if agent, scoped := scopedAgent(r); scoped {
		// Defense in depth behind the agent route allowlist: only an orchestrator.
		if agent.Role != requestscope.RoleOrchestrator {
			writeAgentScopeForbidden(w, r, "install a release: only an orchestrator session may")
			return
		}
	}
	var in ReleaseInstallRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "body must be {\"sha\": \"<40 hex>\"}", nil)
		return
	}
	res, err := c.Svc.Install(r.Context(), strings.TrimSpace(in.SHA))
	if errors.Is(err, releaseinstall.ErrRefused) {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "RELEASE_INSTALL_REFUSED", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, ReleaseInstallResponse{SHA: res.SHA, App: res.App, Log: res.Log, Result: res.ResultFile, PID: res.PID})
}

// requestBuild lets a fenced agent ask for a build without write access to
// release-builds: the daemon runs the script from the project's own checkout.
func (c *ReleaseInstallController) requestBuild(w http.ResponseWriter, r *http.Request) {
	if c.Build == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/release/request-build")
		return
	}
	agent, scoped := scopedAgent(r)
	if !scoped {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "NO_PROJECT", "request-build needs an agent session", nil)
		return
	}
	res, err := c.Build.Request(r.Context(), string(agent.ProjectID))
	if errors.Is(err, releaseinstall.ErrRefused) {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "RELEASE_BUILD_REFUSED", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, ReleaseBuildResponse{Log: res.Log, PID: res.PID})
}
