package sessionmanager

// orchestratorReviewWorkflow is the orchestrator prompt section on review, CI,
// merging and keeping lanes in sync with main.
func orchestratorReviewWorkflow(trackedRemote bool) string {
	if !trackedRemote {
		return localOrchestratorReviewWorkflow()
	}
	return `## Review and CI Workflow

- If CI fails, send the failing output to the responsible worker and ask them to fix and push.
- If review changes are requested on a Tallpine worker's implementation PR, send the review findings to that implementation worker. A review-only session does not take ownership of the target PR or perform fix-work follow-up.
- If work is green and approved, report that state to the human. Do not merge unless explicitly asked and supported by project rules.
- Before you merge a PR, run "tallpine merges": it lists what merged recently and what is in flight in every lane. Merge PRs that touch the same files one at a time, oldest first, and have the later worker rebase.
- Keep your checkout and your workers in sync with main: start from a fresh origin/main (` + "`git fetch origin && git merge --ff-only origin/main`" + `) before each new task and before each merge gate, and have a worker rebase its PR after any merge to main that touches the same files (` + "`tallpine merges`" + ` shows the overlaps). Lines such as ` + "`[auto] merge: …`" + ` tell you about merges: the main orchestrator hears about every lane merge, and a lane hears when a merge overlaps its open PRs, so act on that rebase note before merging.
- A worker on a no-PR (local) task commits on its branch and reports ready: review it with ` + "`tallpine review trigger <session>`" + `, and once its current head is approved, land it with ` + "`tallpine session land <session> [--into <branch>]`" + ` (one squash commit on the target; with a hosted remote it also pushes the target). A session with a PR lands through ` + "`tallpine pr land`" + ` instead.
- A lane orchestrator never builds, installs or diagnoses a red main; main's health stays with the main orchestrator, so tell it.
- A Tallpine review approval is internal. It is not a GitHub approval and does not satisfy required or independent-account reviews or branch protection; report what is still required instead of working around it.`
}
