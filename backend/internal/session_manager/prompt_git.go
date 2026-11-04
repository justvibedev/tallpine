package sessionmanager

func workerGitIsolationPrompt() string {
	return `## Worktree Git Isolation

Tallpine sessions use linked Git worktrees. Linked worktrees share the repository's .git/config and remote definitions with the human checkout. Do not run 'git remote add', 'git remote set-url', 'git remote remove', or write repository config with 'git config --local' (or the default write mode). For session-specific settings, use 'git config --worktree ...'. For a one-off fork push or fetch, use an explicit URL instead of adding a named remote. Existing remotes may be inspected and used read-only.

Upstream tracking is written to the shared .git/config, which a fenced (Security mode) session cannot write. Create branches with 'git switch -c <name> --no-track <base>' and push with 'git push origin HEAD' (no -u, no --set-upstream); do not retry a failed 'could not lock config file' write, the branch and push already succeeded.`
}
