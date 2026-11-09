package httpd

// githubAccountAgentRules: an orchestrator lists the signed-in gh accounts
// (logins only, never tokens) for its own project, to pick one through
// project.githubUser (settings by key).
var githubAccountAgentRules = []agentRule{
	{"GET", "/api/v1/github/accounts", orchestrator, targetNone},
	{"GET", "/api/v1/projects/{id}/github-accounts", orchestrator, targetOwnProject},
}
