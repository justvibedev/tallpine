// Package httpd builds and runs the daemon's HTTP surface: middleware, health
// probes, daemon control, REST APIs, and terminal WebSocket routing.
package httpd

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/buildinfo"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonmeta"
	"tallpine.eu/tallpine/backend/internal/httpd/bodylimit"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
	"tallpine.eu/tallpine/backend/internal/terminal"
)

// ControlDeps carries the daemon-control hooks the router exposes, such as the
// callback that requests a graceful shutdown.
type ControlDeps struct {
	RequestShutdown func()
	// StopAgentsOnShutdown records that the requested shutdown must also stop
	// every agent's processes. POST /shutdown sets it for {"stopAgents":true}.
	StopAgentsOnShutdown func()
	// DaemonToken, when non-empty, makes every loopback request present it in
	// X-VO-Token (see daemonAuthMiddleware). NewWithDeps always sets it unless
	// TALLPINE_DAEMON_AUTH=off; routers built directly in tests opt in explicitly.
	DaemonToken string
	// DaemonTokenPath is named in the 401 message so a caller can find it.
	DaemonTokenPath string
	// RepairDaemonToken rewrites the token file if it vanished while the
	// daemon runs. Called only on a rejected request.
	RepairDaemonToken func()
	// MobileFeatureEnabled reports the mobile feature flag. While it returns
	// false every mobile route except status and the flag setter answers 404.
	// Nil leaves the routes ungated.
	MobileFeatureEnabled func() bool
}

// NewRouterWithControl builds the root router with the standard middleware
// stack, the API surface, and the daemon-control hooks wired from ControlDeps.
// Missing Managers in deps keep routes registered but return OpenAPI-backed 501
// responses.
//
// Middleware order (outermost first):
//
//	RequestID     → attach a request id for correlation
//	requestLogger → slog-backed access log, carries the request id
//	recoverer     → turn a handler panic into 500 instead of crashing the daemon
//	hostAllowlist → loopback-only Host header (DNS-rebinding guard; LAN exempt)
//	daemonAuth    → X-VO-Token on every loopback request (LAN and probes exempt);
//	                scoped agent tokens are authorized against agentRules
//	accountOrigin → exact renderer-origin boundary for Codex account management
//	cors          → CORS allowlist for the Electron renderer / dev origins
//	noStore       → default Cache-Control: no-store on /api/v1/* (handlers may override)
//
// The per-request timeout is deliberately not global: it wraps only bounded
// REST routes, never long-lived terminal streams or health probes.
func NewRouterWithControl(cfg config.Config, log *slog.Logger, termMgr *terminal.Manager, deps APIDeps, control ControlDeps) chi.Router {
	log = loggerOrDefault(log)
	deps = normalizeAPIDeps(deps, log)
	r := chi.NewRouter()
	api := newAPIWithLogger(cfg, deps, log)

	r.Use(middleware.RequestID)
	r.Use(requestLogger(log))
	r.Use(recoverPanics(log))
	// Default request-body cap; handlers with bigger payloads call bodylimit.Raise.
	r.Use(bodylimit.Middleware(bodylimit.Default))
	// DNS-rebinding boundary: refuse non-loopback Hosts before anything else
	// can answer, including CORS preflights.
	r.Use(hostAllowlistMiddleware())
	// Caller authentication: same-user token, before anything can answer.
	// Scoped agent tokens ride the same header and are authorized against
	// the agent capability policy (agentRules).
	if control.DaemonToken != "" {
		tokens := deps.SessionTokens
		if tokens == nil {
			tokens = sessiontoken.New(control.DaemonToken)
		}
		agents := newAgentScopes(tokens, deps.AgentScopes, log)
		if agents != nil {
			agents.approvals = orchestratorApprovalsEnabled(policyDecider(deps.AgentPolicy))
			agents.crossProject = crossProjectEnabled(policyDecider(deps.AgentPolicy))
			agents.devMode = devModeEnabled(policyDecider(deps.AgentPolicy))
			agents.lanesEnabled = orchestratorLanesEnabled(policyDecider(deps.AgentPolicy))
			agents.creators, _ = deps.AgentScopes.(controllers.ProjectCreatorStore)
			agents.evalMarked = projectEvalMarked(deps.AgentScopes)
			agents.peers = deps.PeerCheck
		}
		r.Use(daemonAuthMiddleware(control.DaemonToken, control.DaemonTokenPath, control.RepairDaemonToken, agents, deps.PeerCheck))
	}
	// Credential routes retain their exact renderer-only boundary even for
	// authenticated LAN callers. Check before CORS can answer a preflight.
	r.Use(codexAccountOriginMiddleware(cfg.AllowedOrigins))
	r.Use(corsMiddleware(cfg.AllowedOrigins))
	r.Use(previewOriginMiddleware(api.sessions))
	r.Use(noStoreAPIMiddleware())
	r.Use(mobileFeatureGate(control.MobileFeatureEnabled))
	r.Use(streamShutdownMiddleware)

	// JSON envelopes for unmatched routes / methods — chi's defaults are
	// text/plain, which would break consumers that parse every response as
	// the locked APIError shape.
	r.NotFound(notFoundJSON)
	r.MethodNotAllowed(methodNotAllowedJSON)

	mountHealth(r, cfg)
	mountTerminalMux(r, termMgr, log, cfg.AllowedOrigins)
	mountEventStreams(r, r, log, cfg.AllowedOrigins)
	mountControl(r, control)
	mountPprof(r, control.DaemonToken)
	mountMobile(r, deps.Mobile)
	mountBrowserLive(r, deps.BrowserLive)
	mountAwake(r, &controllers.AwakeController{Svc: deps.Awake})
	mountMobileDevices(r, &controllers.MobileDevicesController{Registry: deps.DeviceRoster, Presence: deps.DeviceLive})
	api.Register(r)

	return r
}

func previewOriginMiddleware(sessions *controllers.SessionsController) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sessions != nil && sessions.PreviewOrigin(w, r) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// mountHealth registers the liveness and readiness probes the Electron
// supervisor polls before letting the renderer connect.
func mountHealth(r chi.Router, cfg config.Config) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		envelope.WriteJSON(w, http.StatusOK, daemonProbePayload("ok", cfg))
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		envelope.WriteJSON(w, http.StatusOK, daemonProbePayload("ready", cfg))
	})
}

// mountControl registers the loopback daemon-control endpoints. /shutdown is
// state-changing, so beyond the install token it is gated by
// localControlRequest to keep a browser the user happens to have open (CSRF / DNS-rebinding) or a
// remote client from being able to kill the daemon.
func mountControl(r chi.Router, deps ControlDeps) {
	if deps.RequestShutdown == nil {
		return
	}
	r.Post("/shutdown", func(w http.ResponseWriter, req *http.Request) {
		if !localControlRequest(req) {
			envelope.WriteJSON(w, http.StatusForbidden, map[string]any{
				"status":  "forbidden",
				"service": daemonmeta.ServiceName,
			})
			return
		}
		// An empty body is the restart path: agents keep running for the next
		// daemon to adopt. {"stopAgents":true} is a deliberate quit or logout:
		// the daemon stops every agent's process tree before it exits.
		var body struct {
			StopAgents bool `json:"stopAgents"`
		}
		raw, err := io.ReadAll(io.LimitReader(req.Body, 4096))
		if err == nil && len(bytes.TrimSpace(raw)) > 0 {
			err = json.Unmarshal(raw, &body)
		}
		if err != nil {
			envelope.WriteJSON(w, http.StatusBadRequest, map[string]any{
				"status":  "invalid_request",
				"service": daemonmeta.ServiceName,
			})
			return
		}
		if body.StopAgents && deps.StopAgentsOnShutdown != nil {
			deps.StopAgentsOnShutdown()
		}
		envelope.WriteJSON(w, http.StatusAccepted, map[string]any{
			"status":     "shutting_down",
			"service":    daemonmeta.ServiceName,
			"pid":        os.Getpid(),
			"stopAgents": body.StopAgents,
		})
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		// Start shutdown only after the accepted response has had a chance to
		// leave this handler. Otherwise http.Server.Shutdown can observe the
		// /shutdown request itself as still active and wait until its graceful
		// deadline under the race detector.
		time.AfterFunc(10*time.Millisecond, deps.RequestShutdown)
	})
}

// mountMobile registers the Connect Mobile control routes: status, enable,
// disable, and regenerate. These toggle the LAN bridge that lets a phone reach
// the daemon. They must be reachable from the desktop renderer — a browser
// context that always sends an Origin header — so they are NOT gated by
// localControlRequest (which rejects any Origin-bearing request and is meant for
// the CLI). The "phone must never toggle its own access" invariant is enforced
// on the LAN listener instead, by lanControlBlock, which 404s /api/v1/mobile on
// the 0.0.0.0 socket the phone reaches — a transport-based check that cannot be
// spoofed with a forged Host header. On the loopback listener these routes are
// protected by the same CORS allowlist as every other app route.
func mountMobile(r chi.Router, c *controllers.MobileController) {
	if c == nil {
		return
	}
	r.Get("/api/v1/mobile/status", c.Status)
	r.Post("/api/v1/mobile/enable", c.Enable)
	r.Post("/api/v1/mobile/enable-lan-only", c.EnableLANOnly)
	r.Post("/api/v1/mobile/enable-tunnel-only", c.EnableTunnelOnly)
	r.Post("/api/v1/mobile/remote-access", c.StartRemoteAccess)
	r.Post("/api/v1/mobile/disable", c.Disable)
	r.Post("/api/v1/mobile/regenerate", c.Regenerate)
	r.Post("/api/v1/mobile/secure-pairing", c.SecurePairing)
	r.Post("/api/v1/mobile/keep-awake", c.KeepAwake)
	r.Post("/api/v1/mobile/browser-control", c.BrowserRemoteAccess)
	r.Post("/api/v1/mobile/device-pairing", c.RequireDevicePairing)
	r.Post("/api/v1/mobile/feature", c.Feature)
	r.Post("/api/v1/mobile/public-url", c.PublicBaseURL)
	r.Post("/api/v1/mobile/quick-tunnel", c.QuickTunnel)
}

// mountMobileDevices registers the desktop-only mobile device roster. These sit
// under /api/v1/mobile deliberately: lanControlBlock already 404s that prefix on
// the LAN socket, so the "a phone must not manage the roster" invariant is
// enforced by the transport rather than by a spoofable header.
//
// The routes are mounted unconditionally, even when c.Registry is nil (a
// corrupt ~/.tallpine/data/mobile/push-devices.json failed to load): each handler
// answers 503 DEVICE_REGISTRY_UNAVAILABLE in that case, so the desktop can tell
// "the registry failed to load" apart from "this route doesn't exist / talking
// to an old daemon" (a 404 would be ambiguous with both). Only a nil controller
// pointer — meaning the roster surface was never wired into APIDeps at all —
// skips mounting, matching mountMobile's convention for an absent controller.
func mountMobileDevices(r chi.Router, c *controllers.MobileDevicesController) {
	if c == nil {
		return
	}
	r.Get("/api/v1/mobile/devices", c.List)
	r.Delete("/api/v1/mobile/devices/{installId}", c.Remove)
}

// localControlRequest reports whether a control request is a trusted local
// caller. The Go CLI client addresses the daemon by its loopback host and
// never sets an Origin header; a cross-site browser fetch always carries an
// Origin, and a DNS-rebinding attempt resolves a non-loopback Host. Rejecting
// either closes the CSRF/rebinding vector while leaving the CLI unaffected.
func localControlRequest(r *http.Request) bool {
	if r.Header.Get("Origin") != "" {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// daemonProbePayload is shared by /healthz and /readyz. Dependency
// initialization happens before the server is constructed, so a listening
// daemon is ready to answer requests.
func daemonProbePayload(status string, cfg config.Config) map[string]any {
	payload := map[string]any{
		"status":  status,
		"service": daemonmeta.ServiceName,
		"pid":     os.Getpid(),
		// Which commit this daemon runs, so release tooling can confirm an
		// upgrade or rollback actually took effect.
		"build": buildinfo.Fields(),
	}
	if exe, err := os.Executable(); err == nil && exe != "" {
		payload["executablePath"] = exe
	}
	if cwd, err := os.Getwd(); err == nil && cwd != "" {
		payload["workingDirectory"] = cwd
	}
	if cfg.StartupWorkingDirectory != "" {
		payload["startupWorkingDirectory"] = cfg.StartupWorkingDirectory
	}
	// TALLPINE_APPIMAGE is set by the Electron app at spawn time when it runs from an
	// AppImage. The value is the stable outer .AppImage file path, which the
	// app's daemon identity check compares instead of the transient
	// /tmp/.mount_* executable path (regenerated on every AppImage launch).
	if appImage := brand.Getenv("APPIMAGE"); appImage != "" {
		payload["appImagePath"] = appImage
	}
	return payload
}

// Wake policy can alter host power and remains desktop/loopback-only.
func mountAwake(r chi.Router, c *controllers.AwakeController) {
	r.Get("/api/v1/awake", c.Status)
	r.Get("/api/v1/awake/observations", c.Observe)
	r.Post("/api/v1/awake/sessions", c.Start)
	r.Post("/api/v1/awake/sessions/{awakeId}/end", c.End)
	r.Post("/api/v1/awake/sessions/{awakeId}/extend", c.Extend)
	r.Put("/api/v1/awake/settings", c.Settings)
	r.Put("/api/v1/awake/rules", c.Rules)
	r.Post("/api/v1/awake/low-power/authorize", c.AuthorizeLowPower)
	r.Post("/api/v1/awake/low-power/revoke", c.RevokeLowPower)
	r.Post("/api/v1/awake/stop", c.Stop)
	r.Post("/api/v1/awake/resume", c.Resume)
	r.Post("/api/v1/awake/recover", c.Recover)
	r.Delete("/api/v1/awake/history", c.ClearHistory)
	r.Post("/api/v1/awake/downloads", c.Downloads)
}
