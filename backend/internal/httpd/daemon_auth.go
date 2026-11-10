package httpd

import (
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/peeradmit"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

// daemonAuthMiddleware requires the per-install token (X-VO-Token, compared in
// constant time) on every request to the loopback listener, HTTP and
// WebSocket alike. Loopback reachability alone is not a principal: other OS
// users, containers reaching the host, and agents all share 127.0.0.1. The
// token lives in a 0600 file only this user can read; the CLI reads it and
// Electron main injects it, so the renderer never holds it.
//
// Requests that arrived through the LAN listener are exempt: they already
// passed bearer auth there. The remaining exemptions are the closed list in
// daemonAuthExempt; any addition needs an ADR update.
//
// A scoped agent token (TALLPINE_SESSION_TOKEN, see agentScopes) in the same header
// is verified and authorized against the agent capability policy instead; it
// never carries the install token's full authority.
//
// tokenPath is only used to tell an unauthenticated caller where the token
// lives; repair rewrites the file if it went missing while the daemon runs.
func daemonAuthMiddleware(token, tokenPath string, repair func(), agents *agentScopes, peers *peeradmit.Checker) func(http.Handler) http.Handler {
	message := "This daemon requires its install token in the " + daemontoken.Header + " header"
	if tokenPath != "" {
		message += " (the contents of " + tokenPath + ")"
	}
	message += ". The " + brand.CLIName + " CLI and the " + brand.ProductName + " desktop app send it automatically; upgrade them if they fail with this error."
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requestscope.IsLAN(r.Context()) {
				next.ServeHTTP(w, r)
				return
			}
			if presentedTokenValid(r, token) {
				if !daemonAuthExempt(r) {
					observePeer(peers, r, peeradmit.Credential{Kind: peeradmit.CredentialInstall})
				}
				next.ServeHTTP(w, r)
				return
			}
			if daemonAuthExempt(r) {
				if unauthenticatedSecretPreviewPath(r) {
					envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
					return
				}
				// The URL is only half the check: a preview origin maps it
				// under its stored entry's directory.
				// The serving handlers check the resolved path.
				next.ServeHTTP(w, r.WithContext(requestscope.WithTokenless(r.Context())))
				return
			}
			if values := r.Header.Values(daemontoken.Header); agents != nil && len(values) == 1 && sessiontoken.IsScoped(values[0]) {
				agents.serve(w, r, values[0], next)
				return
			}
			if repair != nil {
				repair()
			}
			envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "DAEMON_TOKEN_REQUIRED", message, nil)
		})
	}
}

// presentedTokenValid accepts exactly one X-VO-Token value equal to token.
// The token is never accepted from a query string, which access logs record.
func presentedTokenValid(r *http.Request, token string) bool {
	values := r.Header.Values(daemontoken.Header)
	return len(values) == 1 && daemontoken.Equal(values[0], token)
}

// daemonAuthExempt is the complete list of loopback requests served without
// the token. Each one either carries no daemon capability or is already
// confined by its own boundary:
//
//   - GET/HEAD /healthz, /readyz: liveness probes for supervisors and
//     smoke scripts; read-only process identity, no app data.
//   - CORS preflights (OPTIONS + Origin + Access-Control-Request-Method):
//     corsMiddleware answers them (204 or 403) without reaching a handler,
//     and browsers never attach credentials or custom headers to them.
//   - Any request on a preview/artifact host that carries the session's
//     capability label (<label>.<id>.k-<128-bit hex>.localhost, see
//     preview.SetCapabilityKey): previewOriginMiddleware serves it entirely
//     as GET/HEAD file bytes and never routes it to the API. Browser-panel
//     tabs and iframes load these. An id-only host is not exempt.
//   - GET/HEAD render document paths: sandboxed document bytes, addressed by
//     an unguessable (UUID) render id, that iframes and the render check
//     window load without app headers.
//
// The legacy /api/v1/sessions/{id}/preview/files|artifacts/* paths are NOT
// exempt: they are keyed by the guessable session id alone, so they need the
// token (the shell attaches it, daemon-auth.ts). The preview exemption does
// not serve a credential-bearing path either (see
// unauthenticatedSecretPreviewPath).
func daemonAuthExempt(r *http.Request) bool {
	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz"):
		return true
	case r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "":
		return true
	case isPreviewHost(r.Host):
		return true
	case isRenderDocumentRequest(r):
		return true
	}
	return false
}

// unauthenticatedSecretPreviewPath reports whether an exempt preview read
// names a credential-bearing file (previewutil.IsSecretPath: .git, .env,
// .npmrc, keys). Preview reads need no token, and session ids are guessable,
// so without this any local process could read a worktree's secrets. Ordinary
// dotfiles stay readable, because the Browser tab opens workspace-file links
// (.github/workflows/ci.yml, .tallpine/attachments/...) through this path without
// the token. It answers like a missing file, so it is not an existence
// oracle. This rejects early on the URL; the preview handlers repeat the
// check on the resolved asset path (controllers.secretFromTokenless).
func unauthenticatedSecretPreviewPath(r *http.Request) bool {
	asset := r.URL.Path
	if !isPreviewHost(r.Host) {
		rest, ok := strings.CutPrefix(asset, "/api/v1/sessions/")
		if !ok {
			return false
		}
		parts := strings.SplitN(rest, "/", 4)
		if len(parts) != 4 || parts[1] != "preview" || (parts[2] != "files" && parts[2] != "artifacts") {
			return false
		}
		asset = parts[3]
	}
	return previewutil.IsSecretPath(asset)
}
