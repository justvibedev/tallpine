package sessionmanager

// workerMultiPRPrompt explains the branch convention Tallpine uses to attribute pull
// requests to this session. Every shape it recommends must be creatable in Git
// while the session branch exists, and either owned under observe/scm's
// attribution rules or paired with an explicit claim-pr step, which accepts any
// head branch; prompt_branches_test.go checks these against real Git.
func workerMultiPRPrompt() string {
	return `## Pull Requests for This Session

Tallpine automatically attributes PRs to this session when the source branch is this session branch or lives under this session namespace. A branch outside that namespace may need an explicit ` + "`tallpine session claim-pr <full-pr-url>`" + `.

Git cannot create a branch beneath an existing branch, or at a path that already holds branches; it fails with ` + "`cannot lock ref`" + `. Never name a new branch ` + "`<existing-branch>/<topic>`" + `.

- If your current branch ends in ` + "`/root`" + `, create independent PR branches as siblings under the same namespace, for example ` + "`<namespace>/<topic>`" + ` from ` + "`<namespace>/root`" + `. Git rejects ` + "`<namespace>/root/<topic>`" + ` and the bare ` + "`<namespace>`" + ` while the root branch exists. You may also open a PR directly from the ` + "`/root`" + ` branch.
- For a workspace project whose recorded session branch is ` + "`tp/<session-id>`" + ` or a collision variant such as ` + "`tp/<session-id>-2`" + `, use hyphen siblings such as ` + "`<session-branch>-<topic>`" + ` in each registered repository. Keep the full collision suffix. Claim a child-repository PR explicitly with ` + "`tallpine session claim-pr <full-pr-url>`" + ` when needed.
- Otherwise, open the PR from your current session branch. For an additional PR, create ` + "`<current-branch>-<topic>`" + ` in this worktree and attach its PR with ` + "`tallpine session claim-pr <full-pr-url>`" + `, because that name is outside the session's branch namespace.
- To stack a PR on top of another, create the new branch from the parent branch and target the parent branch in the PR. Name it ` + "`<parent-branch>-<topic>`" + `, or ` + "`<namespace>/<topic>`" + ` when the parent is the ` + "`/root`" + ` branch. On a custom session branch, attach a stacked PR with ` + "`tallpine session claim-pr <full-pr-url>`" + ` as well.

Keep to these branch names so Tallpine can track every PR you open.`
}
