package daemon

import (
	"tallpine.eu/tallpine/backend/internal/adapters/reviewer"
	"tallpine.eu/tallpine/backend/internal/adapters/runtime/runtimeselect"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// newReviewerLauncher builds the production reviewer launcher: each option is
// one thing a reviewer shares with the worker it reviews.
func newReviewerLauncher(cfg config.Config, reviewers *reviewer.Resolver, runtime runtimeselect.Runtime, store *sqlite.Store, sessionTokens *sessiontoken.Authority, reviewerAccounts *reviewcore.AccountEnvSlot, githubAccounts sessionmanager.GitHubAccountTokens, agents ports.AgentResolver, agentReadiness ports.AgentReadinessProvider, chat sessionmanager.ChatLauncher) reviewcore.Launcher {
	reviewerChat, _ := chat.(reviewcore.ReviewerChatController)
	return reviewcore.NewLauncher(reviewers, runtime, cfg.DataDir,
		reviewcore.WithRunFilePath(cfg.RunFilePath),
		reviewcore.WithReviewerTokens(sessionTokens),
		reviewcore.WithAgentAccounts(reviewerAccounts),
		reviewcore.WithGitHubAccounts(githubAccounts, store),
		reviewcore.WithReviewerLaunchAuth(reviewerLaunchAuth{agents: agents}),
		reviewcore.WithAgentAuth(reviewerAgentAuth{readiness: agentReadiness}),
		reviewcore.WithPolicy(policyOf(chat)),

		reviewcore.WithReviewerChat(reviewerChat))
}
