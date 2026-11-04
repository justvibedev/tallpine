package sessionmanager

import (
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// projectHasTrackedRemote reports whether the project has a hosted remote that
// PRs, pushes and fetches make sense against (see domain.HasHostedRemote).
func projectHasTrackedRemote(project promptProject) bool {
	return domain.HasHostedRemote(project.Repo)
}

const trackedBaseRefLine = "The project path is the owner's checkout and can be behind origin/main; work from your own worktree updated to origin/main, and don't hand the owner commands that run from the project path."

// projectBaseRefLine is the last bullet of the project context section.
func projectBaseRefLine(project promptProject) string {
	if projectHasTrackedRemote(project) {
		return trackedBaseRefLine
	}
	branch := localBranchName(project)
	return fmt.Sprintf("The project path is the owner's checkout and can be behind %[1]s; work from your own worktree updated to %[1]s, and don't hand the owner commands that run from the project path.", branch)
}

const trackedSyncLine = "- Keep your checkout and your workers in sync with main: start from a fresh origin/main (`git fetch origin && git merge --ff-only origin/main`) before each new task and before each merge gate, and have a worker rebase its PR after any merge to main that touches the same files"

const localSyncLine = "- Keep your checkout and your workers in sync with main: there is no remote to fetch, so start from the local default branch (`git merge --ff-only <default branch>`) before each new task and before each merge gate, and have a worker rebase after any merge to it that touches the same files"

// localOrchestratorReviewWorkflow derives the remoteless workflow from the
// tracked one so the two cannot drift: only the origin sync bullet changes.
func localOrchestratorReviewWorkflow() string {
	return strings.Replace(orchestratorReviewWorkflow(true), trackedSyncLine, localSyncLine, 1)
}

const trackedWorktreeLine = "fast-forwarded to origin's default branch only when you start or are restored. Before running repo scripts or reading code to give the owner instructions, update your worktree to origin's default branch."

// localizeOrchestratorBase rewrites the worktree-refresh bullet for projects
// with no tracked remote, so it names the local default branch.
func localizeOrchestratorBase(project promptProject, prompt string) string {
	if projectHasTrackedRemote(project) {
		return prompt
	}
	branch := localBranchName(project)
	local := fmt.Sprintf("based on %[1]s only when you start or are restored. Before running repo scripts or reading code to give the owner instructions, update your worktree to %[1]s.", branch)
	return strings.Replace(prompt, trackedWorktreeLine, local, 1)
}

// localBranchName names the local base ref. An unresolved ("auto") default
// branch is never guessed, so the wording stays neutral.
func localBranchName(project promptProject) string {
	if b := strings.TrimSpace(project.DefaultBranch); b != "" {
		return "local " + b
	}
	return "the local default branch"
}

const trackedPushClause = " and push with 'git push origin HEAD' (no -u, no --set-upstream); do not retry a failed 'could not lock config file' write, the branch and push already succeeded."

// localizeWorkerGitIsolation drops the push instruction when there is no
// tracked remote: workers never push to a local-folder remote.
func localizeWorkerGitIsolation(project promptProject, prompt string) string {
	if projectHasTrackedRemote(project) {
		return prompt
	}
	return strings.Replace(prompt, trackedPushClause, "; do not retry a failed 'could not lock config file' write, the branch already succeeded. Do not push: there is no hosted remote.", 1)
}

// workerLocalReviewPrompt tells a worker what to do when its task says "no PR":
// commit, report ready, and let the branch be reviewed and landed locally. It
// applies in every project, with or without a remote; the brief picks the flow.
func workerLocalReviewPrompt() string {
	return "## Tasks without a PR\n" +
		"When your brief says local, no PR, or do not push: commit your work on your own branch, do not push it and do not open a PR, then report ready with `tallpine report --needs-input`. Your branch gets a Tallpine native review of base...branch (no GitHub involved); a changes-requested verdict arrives as a message carrying the findings as `path:line - finding` lines. Fix each one, commit again, and report ready again. The orchestrator or owner lands the approved branch into the target branch; you do not."
}
