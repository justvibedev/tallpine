package httpd

// agentRulesWithRelease is baseAgentRules plus the release routes; agentRules
// adds the land routes.
var agentRulesWithRelease = append(baseAgentRules[:len(baseAgentRules):len(baseAgentRules)], releaseAgentRules...)

var agentRules = append(agentRulesWithRelease, landAgentRules...)

// releaseAgentRules: the daemon builds and installs local releases for agents
// whose fenced shells cannot write release-builds or read the daemon token.
var releaseAgentRules = []agentRule{
	{"POST", "/api/v1/release/install", orchestrator, targetNone},
	{"POST", "/api/v1/release/request-build", workerUp, targetNone},
}
