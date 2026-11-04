package httpd

// landAgentRules: landing a no-PR branch moves the target branch, so only an
// orchestrator of the session's project may, never a worker (the owner needs
// no token).
var landAgentRules = []agentRule{
	{"POST", "/api/v1/sessions/{sessionId}/land", orchestrator, targetProject},
}
