package httpd

import (
	"net/http"
	"strings"
)

// lanRouteClass says who may call a route through the network-facing listener
// (LAN, quick tunnel, Tailscale Serve). Anything not listed is unclassified and
// treated as loopback-only at runtime; the invariant test in
// lan_route_policy_test.go fails until a new route is added to the table.
type lanRouteClass int

const (
	lanRouteUnclassified lanRouteClass = iota
	// lanRouteMobile is part of what the phone app uses.
	lanRouteMobile
	// lanRouteLoopbackOnly is admin, host-mutating or agent-internal: it answers
	// 404 on the network listener, as if it were never mounted.
	lanRouteLoopbackOnly
)

const (
	anyMethod = ""
	readOnly  = "GET HEAD"
)

// lanRouteRule matches by method set and path pattern. In a pattern "*" is one
// segment and a trailing "**" is any remainder (including nothing). The first
// matching rule wins, so narrow loopback-only rules go before the broad mobile
// rule they carve out of.
type lanRouteRule struct {
	methods string // space-separated, or anyMethod
	pattern string
	class   lanRouteClass
}

func mobile(methods, pattern string) lanRouteRule {
	return lanRouteRule{methods, pattern, lanRouteMobile}
}

func loopback(methods, pattern string) lanRouteRule {
	return lanRouteRule{methods, pattern, lanRouteLoopbackOnly}
}

// lanRouteTable is the single classification of every mounted route. The
// mobile set is derived from packages/mobile (lib/api.ts, lib/chat/api.ts,
// lib/connectRuntime.ts, lib/pairExchange.ts). Plain-HTTP write refusal
// (lan_agent_drive_block.go) and shell-route refusal (lan_shell_block.go) are
// separate, later checks on the routes classified mobile here.
var lanRouteTable = []lanRouteRule{
	// Daemon control and developer surfaces.
	loopback(anyMethod, "/shutdown"),
	loopback(anyMethod, "/internal/**"),
	loopback(anyMethod, "/debug/**"),
	loopback(anyMethod, "/api/v1/awake/**"),
	loopback(anyMethod, "/api/v1/mobile/**"),
	loopback(anyMethod, "/api/v1/dev/**"),
	loopback(anyMethod, "/api/v1/browser/**"),
	loopback(anyMethod, "/api/v1/desktop/**"),
	loopback(anyMethod, "/api/v1/side-chats/**"),
	loopback(anyMethod, "/api/v1/system/**"),

	// Probes, discovery, pairing.
	mobile(readOnly, "/healthz"),
	mobile(readOnly, "/readyz"),
	mobile(readOnly, "/api/v1/identity"),
	mobile(readOnly, "/api/v1/endpoints"),
	mobile(readOnly, "/api/v1/openapi.yaml"),
	mobile(readOnly, "/api/v1/link-preview"),
	mobile(readOnly, "/api/v1/health/**"),
	mobile(readOnly, "/api/v1/events"),
	mobile(readOnly, "/api/v1/streams"),
	mobile(readOnly, "/api/v1/usage/**"),
	mobile(anyMethod, "/pair/exchange"),
	mobile(anyMethod, "/api/v1/tunnel"),

	// Terminals (refused on plain transports by lan_shell_block.go).
	mobile(anyMethod, "/mux"),
	mobile(anyMethod, "/api/v1/shell-terminals"),
	mobile(anyMethod, "/api/v1/shell-terminals/*"),

	// Sessions. Carve-outs first: credentials, policy and review-owner changes,
	// and routes only an agent or the desktop uses.
	loopback(anyMethod, "/api/v1/sessions/cleanup"),
	mobile(readOnly, "/api/v1/sessions/*/browser/live"),
	loopback(anyMethod, "/api/v1/sessions/*/github-credential"),
	loopback(anyMethod, "/api/v1/sessions/*/commit"),
	loopback(anyMethod, "/api/v1/sessions/*/land"),
	loopback(anyMethod, "/api/v1/sessions/*/agent-policy"),
	loopback(anyMethod, "/api/v1/sessions/*/merge-policy"),
	loopback(anyMethod, "/api/v1/sessions/*/provider-switch"),
	loopback(anyMethod, "/api/v1/sessions/*/reviewer"),
	loopback(anyMethod, "/api/v1/sessions/*/activity"),
	loopback(anyMethod, "/api/v1/sessions/*/agent-browser/**"),
	loopback(anyMethod, "/api/v1/sessions/*/exit-agent"),
	loopback(anyMethod, "/api/v1/sessions/*/rollback"),
	loopback(anyMethod, "/api/v1/sessions/*/background-tasks/stop"),
	loopback(anyMethod, "/api/v1/sessions/*/pr/claim"),
	loopback(anyMethod, "/api/v1/sessions/*/preview/server"),
	loopback(anyMethod, "/api/v1/sessions/*/renders"),
	loopback(anyMethod, "/api/v1/sessions/*/renders/*/artifact"),
	loopback(anyMethod, "/api/v1/sessions/*/renders/check"),
	loopback(anyMethod, "/api/v1/sessions/*/artifact-files"),
	loopback(anyMethod, "/api/v1/sessions/*/artifact-versions/restore"),
	loopback(anyMethod, "/api/v1/sessions/*/artifact-versions/sync"),
	loopback("PUT POST PATCH DELETE", "/api/v1/sessions/*/workspace/**"),
	loopback(anyMethod, "/api/v1/sessions/*/reviews/submit"),
	mobile(anyMethod, "/api/v1/sessions"),
	mobile(anyMethod, "/api/v1/sessions/*"),
	mobile(anyMethod, "/api/v1/sessions/*/conversation/**"),
	mobile(anyMethod, "/api/v1/sessions/*/interface-transition"),
	mobile(anyMethod, "/api/v1/sessions/*/interface-transition/*/notice-acknowledgement"),
	mobile(anyMethod, "/api/v1/sessions/*/pin"),
	mobile(anyMethod, "/api/v1/sessions/*/preview"),
	mobile(anyMethod, "/api/v1/sessions/*/preview/app/**"),
	mobile(readOnly, "/api/v1/sessions/*/preview/artifacts/**"),
	mobile(readOnly, "/api/v1/sessions/*/preview/file-url"),
	mobile(readOnly, "/api/v1/sessions/*/preview/files/**"),
	mobile(readOnly, "/api/v1/sessions/*/artifact-files/**"),
	mobile(readOnly, "/api/v1/sessions/*/artifact-versions"),
	mobile(readOnly, "/api/v1/sessions/*/artifact-versions/*"),
	mobile(readOnly, "/api/v1/sessions/*/renders/*"),
	mobile(readOnly, "/api/v1/sessions/*/history"),
	mobile(readOnly, "/api/v1/sessions/*/usage"),
	mobile(readOnly, "/api/v1/sessions/*/workspace/**"),
	mobile(readOnly, "/api/v1/sessions/*/pr"),
	mobile(readOnly, "/api/v1/sessions/*/pr/**"),
	mobile("POST", "/api/v1/sessions/*/attachments"),
	mobile("POST", "/api/v1/sessions/*/chat-view"),
	mobile("POST", "/api/v1/sessions/*/send"),
	mobile("POST", "/api/v1/sessions/*/kill"),
	mobile("POST", "/api/v1/sessions/*/restore"),
	mobile("POST", "/api/v1/sessions/*/resume-agent"),
	mobile("PUT PATCH", "/api/v1/sessions/*/auto-review"),
	mobile("PATCH", "/api/v1/sessions/*/auto-inject-ci"),
	mobile("PATCH", "/api/v1/sessions/*/auto-inject-review"),
	mobile(anyMethod, "/api/v1/sessions/*/reviews"),
	mobile(anyMethod, "/api/v1/sessions/*/reviews/*"),
	mobile(anyMethod, "/api/v1/sessions/*/reviews/comments/resolve"),

	// Reviews (the reviewer chat), orchestrators, PRs, notifications, push.
	mobile(anyMethod, "/api/v1/reviews/*/conversation/**"),
	mobile(anyMethod, "/api/v1/orchestrators"),
	mobile(anyMethod, "/api/v1/orchestrators/*"),
	mobile(anyMethod, "/api/v1/orchestrators/*/handoff"),
	mobile(anyMethod, "/api/v1/orchestrators/*/research"),
	mobile(anyMethod, "/api/v1/orchestrators/delegate"),
	mobile("POST", "/api/v1/prs/*/merge"),
	mobile("POST", "/api/v1/prs/*/resolve-comments"),
	mobile(anyMethod, "/api/v1/notifications"),
	mobile(anyMethod, "/api/v1/notifications/*"),
	mobile(anyMethod, "/api/v1/notifications/read-all"),
	mobile(anyMethod, "/api/v1/notifications/stream"),
	mobile(anyMethod, "/api/v1/push/devices"),
	mobile(anyMethod, "/api/v1/push/devices/*"),
	mobile(anyMethod, "/api/v1/push/pairings/*"),

	// Agents: model pickers and chat availability only. Install, auth, logout,
	// probe, verify and account routes stay loopback-only.
	mobile(readOnly, "/api/v1/agents"),
	mobile(readOnly, "/api/v1/agents/readiness"),
	mobile(readOnly, "/api/v1/agents/auth-plans"),
	mobile(readOnly, "/api/v1/agents/chat/**"),
	mobile(readOnly, "/api/v1/agents/*/models"),
	mobile(readOnly, "/api/v1/agents/*/update-advisory"),
	mobile("POST", "/api/v1/agents/refresh"),
	mobile("POST", "/api/v1/agents/*/models/refresh"),
	mobile("POST", "/api/v1/agents/*/chat/prewarm"),
	loopback(anyMethod, "/api/v1/agents/**"),

	// Read-only project and settings views. Project config (env, Setup and
	// Cleanup steps), agent policy, permissions, skills and cues are
	// loopback-only (backlog too, except the one list GET below), as are the GitHub token and account routes.
	mobile(readOnly, "/api/v1/projects"),
	mobile(readOnly, "/api/v1/projects/*"),
	// The cross-lane merge digest: a read-only GET, nothing else under a project.
	mobile("GET", "/api/v1/projects/*/merges"),
	// The project backlog list, read-only. Item reads and every write stay loopback-only.
	mobile("GET", "/api/v1/projects/*/backlog"),
	mobile(readOnly, "/api/v1/settings"),
	loopback(anyMethod, "/api/v1/settings/**"),
	loopback(anyMethod, "/api/v1/projects/**"),
	loopback(anyMethod, "/api/v1/github/**"),
	// Installing a local release build runs the installer on this Mac (B433).
	loopback(anyMethod, "/api/v1/release/**"),
	mobile("GET POST", "/api/v1/update"),

	// Everything else the desktop owns.
	loopback(anyMethod, "/api/v1/automations/**"),
	loopback(anyMethod, "/api/v1/automations"),
	loopback(anyMethod, "/api/v1/cleanup/**"),
	loopback(anyMethod, "/api/v1/cleanup"),
	loopback(anyMethod, "/api/v1/storage/**"),
	loopback(anyMethod, "/api/v1/storage"),
	loopback(anyMethod, "/api/v1/cues/**"),
	loopback(anyMethod, "/api/v1/artifacts/**"),
	loopback(anyMethod, "/api/v1/backlog/**"),
	loopback(anyMethod, "/api/v1/fs/**"),
	loopback(anyMethod, "/api/v1/imports/**"),
	loopback(anyMethod, "/api/v1/reports"),
	loopback(anyMethod, "/api/v1/reviews/*/activity"),
	loopback(anyMethod, "/api/v1/task-preparations/**"),
}

func methodIn(set, method string) bool {
	if set == anyMethod {
		return true
	}
	for _, m := range strings.Fields(set) {
		if m == method {
			return true
		}
	}
	return false
}

func patternMatches(pattern, path string) bool {
	p := strings.Split(strings.Trim(pattern, "/"), "/")
	s := strings.Split(strings.Trim(path, "/"), "/")
	for i, seg := range p {
		if seg == "**" {
			return true
		}
		if i >= len(s) || (seg != "*" && seg != s[i]) || (seg == "*" && s[i] == "") {
			return false
		}
	}
	return len(s) == len(p)
}

// classifyLANRoute returns the class of the first rule that matches.
func classifyLANRoute(method, path string) lanRouteClass {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	for _, rule := range lanRouteTable {
		if methodIn(rule.methods, method) && patternMatches(rule.pattern, path) {
			return rule.class
		}
	}
	return lanRouteUnclassified
}

// lanRouteAllowed is the runtime gate: only routes classified mobile pass.
func lanRouteAllowed(method, path string) bool {
	return classifyLANRoute(method, path) == lanRouteMobile
}
