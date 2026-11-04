package httpd

import (
	"context"
	pathpkg "path"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/devmode"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

// devModeEnabled reads the devMode policy from the live engine on every
// request. No engine means off.
func devModeEnabled(decider controllers.PolicyDecider) func(context.Context, domain.ProjectID) bool {
	if decider == nil {
		return nil
	}
	return func(ctx context.Context, project domain.ProjectID) bool {
		return devmode.Of(decider.Decide(ctx, agentpolicy.SessionRef{Project: project})).Enabled()
	}
}

// devModeDeniedPrefixes are routes Dev mode does not lift: they return or mint
// owner or device credentials (the Connect Mobile pairing password, the install
// shutdown, desktop and system control). An agent that saved one could act as
// the owner after Dev mode is switched off, so they stay "not available to
// agents" (lan_route_policy.go lists the loopback-only set).
var devModeDeniedPrefixes = []string{
	"/api/v1/mobile", "/api/v1/push", "/api/v1/desktop", "/api/v1/system", "/api/v1/dev", "/api/v1/browser",
	"/internal", "/debug", "/shutdown",
}

// devModeDenied reports whether path stays refused to a Dev-mode orchestrator.
func devModeDenied(path string) bool {
	path = pathpkg.Clean("/" + path)
	if strings.HasPrefix(path, "/api/v1/sessions/") && strings.HasSuffix(path, "/github-credential") {
		return true
	}
	for _, p := range devModeDeniedPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
