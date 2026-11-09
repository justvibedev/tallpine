package sessionmanager

func workerGitIsolationPrompt() string {
	return `## Worktree Git Isolation

Tallpine sessions use linked Git worktrees. Linked worktrees share the repository's .git/config and remote definitions with the human checkout. Do not run 'git remote add', 'git remote set-url', 'git remote rename', 'git remote remove', or write repository config with 'git config --local' (or the default write mode). For session-specific settings, use 'git config --worktree ...'. For a one-off push or fetch, use an explicit URL instead of adding a named remote, and only for a repository the project already uses or one the owner named. Existing remotes may be inspected and used read-only. Never create a repository on a new external host or push source or branches to a repository the project does not already use (a new host, or an unrelated repo on a known host) unless the owner asked for it; never push with '--all' or '--mirror', which publish internal tp/* branches.

Upstream tracking is written to the shared .git/config, which a fenced (Security mode) session cannot write. Create branches with 'git switch -c <name> --no-track <base>' and push with 'git push origin HEAD' (no -u, no --set-upstream); do not retry a failed 'could not lock config file' write, the branch and push already succeeded.`
}

// orchestratorGitIsolationPrompt keeps orchestrators from mutating the owner's
// checkout (linked worktrees share its .git/config) or publishing source to a
// new external host on their own initiative (B593).
func orchestratorGitIsolationPrompt() string {
	return `## Owner Checkout and Git Remotes

The project checkout belongs to the owner, and linked worktrees share its .git/config and remotes. Never run 'git remote add', 'git remote set-url', 'git remote rename' or 'git remote remove' unless the owner explicitly asked for that change in this conversation, and never write repository config ('git config', 'git config --local') from your session or any worktree; use 'git config --worktree' for session-only settings.

Never create a repository on a new external host, add a remote, or push source or branches to a repository the project does not already use (a new host, or an unrelated repo on a known host) unless the owner asked for it in this conversation. Never use 'git push --all' or '--mirror': that publishes internal tp/* branches. These bans also apply to work you delegate: never ask or allow a worker to do any of it. If the owner asks for a remote change, make exactly that change and report it. For one-off pushes, workers may use an explicit URL only for a repository the project already uses or one the owner named.`
}

// orchestratorGitHubAccountPrompt tells orchestrators how to pick a GitHub
// account for their project, and that they may do so only when the owner asks.
func orchestratorGitHubAccountPrompt() string {
	return `## GitHub Account

Change this project's GitHub account only when the owner asked for it in this conversation. List the accounts signed in to gh with 'tallpine github accounts' (add '--project <your project id>' to see which can push to this repo and which one auto-pick chooses). Pin one with 'tallpine project set-config <your project id> --github-user <login>'; '--github-user ""' returns to auto-pick. Never use 'gh auth switch', 'gh auth login' or 'gh auth logout' for this: they change the machine default for every session and project. A newly pinned account reaches only sessions started afterwards, so say so to the owner. You may do this for your own project only.`
}
