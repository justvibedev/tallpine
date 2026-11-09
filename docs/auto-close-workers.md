# Auto-closing finished workers

A worker session ends on its own once its pull requests are done. The close is
the same teardown as an explicit kill (a user kill); a closed
session can still be restored.

## When it fires

On an observation of a merged or closed PR (the SCM observer's existing
reaction, `maybeAutoClose` in `backend/internal/lifecycle/auto_close.go`) and
again each time the worker settles (an idle signal, or its agent process
exiting), the worker is closed only when all of these hold. The observer does
not re-report an unchanged merged PR, so the settle signals are the retry; there
is no separate poller:

- the session's end-on-merge flag is on (it is for new workers unless the
  project turned it off);
- it is a worker, never an orchestrator;
- at least one of its PRs merged and none is still open (closed-unmerged PRs
  count as done, but a session whose PRs were all closed unmerged stays);
- the agent is idle: not working and not waiting on the person;
- no message is queued for it. A queued message holds the close, and the idle
  signal after the queue has drained retries it.

The owning orchestrator (the worker's lane orchestrator, else the project's)
gets `[auto] session_closed: <session id> ended on its own ...` (or, when that orchestrator's own merge led to the close, `was closed right after the PR you merged ...`).

A session that auto-close ended has its end-on-merge flag cleared, so restoring
it by hand does not end it again. Sessions saved and restored across an app
restart keep the flag.

## Setting

`project.terminateOnPrMerge`, on by default. Turn it off per project in
Settings, Project, "End workers when their PRs merge", or with
`tallpine project config`. A managed policy can lock it. The value is copied to
each worker at spawn, so a change applies to workers started afterwards;
workers already running keep the value they started with.

## Workers without a PR

Nothing closes a worker that never opens a PR: "no PR yet" cannot be told
apart from "will never have one", and a one-off worker's final answer is the
deliverable the orchestrator may still want to read in the live session. The
safe signal would be an explicit one: the worker's `tallpine report --done`
that the orchestrator has acknowledged. That is not built here.
