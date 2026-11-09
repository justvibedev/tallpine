package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/githubcred"
)

// GitHubCredentialService issues a GitHub credential for a session on a host.
type GitHubCredentialService interface {
	Issue(ctx context.Context, session domain.SessionID, host string) (githubcred.Credential, error)
	ports.ProjectGitCredentials
}

// GitHubCredentialController serves the credential helper and the gh shim.
type GitHubCredentialController struct {
	Svc GitHubCredentialService
}

// Register mounts the route. Agents reach it with their scoped session token,
// for their own session only (see httpd/agent_scope.go).
func (c *GitHubCredentialController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/github-credential", c.issue)
}

func (c *GitHubCredentialController) issue(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/github-credential")
		return
	}
	var req GitHubCredentialRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	cred, err := c.Svc.Issue(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), req.Host)
	switch {
	case err == nil:
		w.Header().Set("Cache-Control", "no-store")
		envelope.WriteJSON(w, http.StatusOK, GitHubCredentialResponse{Username: cred.Username, Token: cred.Token, Source: cred.Source})
	case errors.Is(err, githubcred.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "No such session", nil)
	case errors.Is(err, githubcred.ErrDenied):
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "GITHUB_CREDENTIALS_DENIED", "GitHub credentials are disabled by the agent policy", nil)
	case errors.Is(err, githubcred.ErrHostNotAllowed):
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "GITHUB_HOST_NOT_ALLOWED", "That host is not one of this project's GitHub hosts", nil)
	case errors.Is(err, githubcred.ErrUnavailable):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "GITHUB_CREDENTIAL_UNAVAILABLE", "No GitHub token is available for this project", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}
