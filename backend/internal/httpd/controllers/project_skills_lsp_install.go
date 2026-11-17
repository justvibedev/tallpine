package controllers

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// ProjectLSPInstallRequest picks what to install by catalog ids only; the
// daemon runs the fixed commands of that entry and never a client command.
type ProjectLSPInstallRequest struct {
	// Language is an LSP catalog language, e.g. "go".
	Language string `json:"language"`
	// Target is the Claude plugin, the language server binary, or all: each
	// that is missing, plugin first (what switching a plugin on starts).
	Target string `json:"target" enum:"plugin,binary,go,all"`
}

// ProjectLSPInstallJob is one install run.
type ProjectLSPInstallJob struct {
	Language string `json:"language"`
	Target   string `json:"target" enum:"plugin,binary,go,all"`
	State    string `json:"state" enum:"running,succeeded,failed"`
	// Command is the command being run, for display.
	Command string `json:"command"`
	// Output is the tail of the combined output.
	Output string `json:"output"`
	// Error says why a failed job failed.
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// ProjectLSPInstallStatus is the project's current or last install job.
type ProjectLSPInstallStatus struct {
	Job *ProjectLSPInstallJob `json:"job,omitempty"`
}

// ClaudeSessionEnv is the Claude account env new sessions launch with.
type ClaudeSessionEnv func(ctx context.Context) (map[string]string, error)

func (c *ProjectSkillsController) registerLSPInstall(r chi.Router) {
	r.Get("/projects/{id}/skills/lsp/install", c.lspInstallStatus)
	r.Post("/projects/{id}/skills/lsp/install", c.lspInstall)
}

func (c *ProjectSkillsController) lspInstallStatus(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil || c.LSPInstaller == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}/skills/lsp/install")
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	out := ProjectLSPInstallStatus{}
	if job, found := c.LSPInstaller.Job(string(p.id)); found {
		out.Job = lspJobView(job)
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *ProjectSkillsController) lspInstall(w http.ResponseWriter, r *http.Request) {
	if c.Projects == nil || c.LSPInstaller == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{id}/skills/lsp/install")
		return
	}
	var in ProjectLSPInstallRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "LSP_INSTALL_INVALID", "invalid request body", nil)
		return
	}
	p, ok := c.scan(w, r)
	if !ok {
		return
	}
	env := maps.Clone(p.env)
	if env == nil {
		env = map[string]string{}
	}
	if in.Target != skillbridge.LSPTargetBinary && in.Target != skillbridge.LSPTargetGo && c.ClaudeSessionEnv != nil {
		// Install where Tallpine's Claude sessions read plugins: the account
		// folder they launch under wins over the project's own config dir.
		account, err := c.ClaudeSessionEnv(r.Context())
		if err != nil && len(account) == 0 {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "LSP_INSTALL_ACCOUNT", "could not resolve the Claude account Tallpine sessions use: "+err.Error(), nil)
			return
		}
		maps.Copy(env, account)
	}
	opts := skillbridge.OptionsFor(c.DataDir, p.path, env)
	job, err := c.LSPInstaller.Start(skillbridge.LSPInstallRequest{
		Project: string(p.id), Language: in.Language, Target: in.Target,
		Env: env, Home: opts.Home, ClaudeConfigDir: opts.ClaudeConfigDir,
	})
	switch {
	case errors.Is(err, skillbridge.ErrLSPUnknown):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "LSP_INSTALL_UNKNOWN", err.Error(), nil)
	case errors.Is(err, skillbridge.ErrLSPNotInstallable):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "LSP_INSTALL_UNSUPPORTED", err.Error(), nil)
	case errors.Is(err, skillbridge.ErrLSPInstallBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "LSP_INSTALL_BUSY", err.Error(), nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "LSP_INSTALL_FAILED", err.Error(), nil)
	default:
		envelope.WriteJSON(w, http.StatusAccepted, ProjectLSPInstallStatus{Job: lspJobView(job)})
	}
}

func lspJobView(j skillbridge.LSPInstallJob) *ProjectLSPInstallJob {
	v := &ProjectLSPInstallJob{Language: j.Language, Target: j.Target, State: j.State, Command: j.Command, Output: j.Output, Error: j.Error, StartedAt: j.StartedAt}
	if !j.FinishedAt.IsZero() {
		finished := j.FinishedAt
		v.FinishedAt = &finished
	}
	return v
}
