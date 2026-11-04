# tallpine review

Manage Tallpine's native code reviewer for a worker's PR.

Tallpine's reviewer is an isolated reviewer agent for implementation PRs owned by an
Tallpine worker. To review that worker's PR, use `tallpine review trigger`. Inside a worker,
the review commands default to the calling session (`TALLPINE_SESSION_ID`), so
`tallpine review trigger` reviews your own implementation PR. An orchestrator passes
the implementation worker's session id.

For a requested review of someone else's PR, spawn or use a normal review-only
worker without claiming or attaching the target PR. That worker submits the
requested provider review and comments directly. Do not run `tallpine review trigger`,
create an ownership-only placeholder, or require an implementation owner just
to review the PR.

Tallpine's review is internal. A Tallpine approval is not a GitHub approval: it does not
satisfy required or independent-account reviews or branch protection, and it
does not authorize merging.

## Syntax

```
tallpine review <subcommand> [args] [flags]
```

The review loop can be inspected, submitted, cancelled, or triggered again.

## Subcommands

---

### tallpine review ls

List review runs for a worker session (default: the calling session). Alias: `list`.

**Syntax:**
```
tallpine review ls [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output reviews as JSON | - |

The first table has one row per PR. When several reviewers reviewed the same
head (for example `--agent codex` and then `--agent claude-code --rerun`),
STATUS shows `running` while any of them is still working, and both STATUS and
VERDICT show `changes_requested` if any reviewer's latest verdict requests
changes, even when another reviewer approved. A second table lists every pass
on each PR's current head: reviewer (agent and model, when Tallpine knows the
model), status, verdict, start time and run id, followed by the reason for each
failed or cancelled pass. With `--json`, each PR carries `currentHeadRuns`
(every pass on the current head, newest first) next to `latestRun`, which is
kept for compatibility and shows only one pass.

**Example:**

```bash
tallpine review ls mer-3
```

```
PR    STATUS             VERDICT            TITLE
#101  changes_requested  changes_requested  Fail reviewer passes

PR    REVIEWER            STATUS    VERDICT            STARTED               RUN
#101  claude-code (opus)  complete  approved           2026-10-26 09:05 UTC  run-2
#101  codex               complete  changes_requested  2026-10-26 09:00 UTC  run-1
```

---

### tallpine review submit

Record a reviewer's result for a worker's PR.

**Syntax:**
```
tallpine review submit [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--body string` | Review body: a path to a Markdown file, or `-` to read from stdin | - |
| `--review-id string` | Id of the GitHub PR review just posted (the `.id` from the `gh api` POST that created the review) | - |
| `--reviews string` | JSON review results array or object: a path, or `-` to read from stdin | - |
| `--run string` | Review run id | Required |
| `--session string` | Worker session id (or pass it as the positional argument) | - |
| `--verdict string` | Review verdict: `approved` or `changes_requested` | Required |

If the local daemon is restarting when a result is submitted, Tallpine retains the
parsed result in memory and retries the same idempotent request for up to 30
seconds. Validation errors return immediately. If the daemon remains unavailable,
the command reports failure and can be repeated safely; daemon idempotency
handles the case where an earlier connection dropped after committing the result.

**Examples:**

```bash
# Submit an approved review for session mer-3
tallpine review submit mer-3 --run review-run-1 --verdict approved
```

```bash
# Submit a changes-requested review with a body from stdin
echo "Please fix the null check on line 42." | tallpine review submit --session mer-3 --run review-run-1 --verdict changes_requested --body -
```

---

### tallpine review cancel

Cancel every running review for a worker's PR (default: the calling session). Alias: `stop`.

**Syntax:**
```
tallpine review cancel [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Worker session id (or pass it positionally) | - |

**Example:**

```bash
tallpine review cancel mer-3
```

---

### tallpine review trigger

Start Tallpine's reviewer on a worker's open PR heads (default: the calling session).
Aliases: `execute`, `restart`. Reviewer panes cannot run it.

**Syntax:**
```
tallpine review trigger [worker-session-id] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--session string` | Worker session id (or pass it positionally) | The calling Tallpine session |
| `--pr string` | Review only this session's implementation PR URL, attaching it to the session first if Tallpine does not track it yet (never from another active session). Do not pass an external review target | Every eligible PR on the session |
| `--agent string` | Reviewer agent for this pass only (alias `--harness`) | Session reviewer, then project reviewer, then the project's default worker agent |
| `--model string` | Reviewer model for this pass only | As above |
| `--effort string` | Reviewer reasoning effort for this pass only | As above |
| `--rerun` | Review a head that already has a review again, or add a reviewer with a different agent alongside one that is still running | - |
| `--no-inject` | Leave the session's review auto-inject setting unchanged | Auto-inject is turned on |

Tallpine fetches the session's PRs fresh from the provider before deciding what is
due, so the pass covers the commit really on the PR. Right after opening a PR,
pass `--pr <url>`.

A head that is already being reviewed, or already has a review, is not reviewed
again: the command exits 1 with `REVIEW_ALREADY_RUNNING` or
`REVIEW_HEAD_ALREADY_REVIEWED` and says what to do. Push new commits, or pass
`--rerun`. The trigger re-reads the PR from the provider first; if you
just pushed and it still says already reviewed, wait a few seconds and retry. The same reviewer agent never runs twice on one head at the same time.

By default the command turns on the worker session's review auto-inject. The
reviewer posts an inline GitHub comment for each change it requires, and Tallpine
forwards those comments to the worker like any other review comments. An
approval adds no comments, so check the verdict with `tallpine review ls`.

A run fails on its own when the reviewer process exits, when a terminal
reviewer sits idle at its prompt for 10 minutes without submitting
(`TALLPINE_REVIEW_IDLE_GRACE` on the daemon raises this for reviewers that wait on
long background work; `off` disables it), or when it has not submitted a
result 45 minutes after it started, in Chat or terminal mode (for example a
reviewer wedged mid-turn, or a launch or restore that hangs;
`TALLPINE_REVIEW_PASS_DEADLINE` changes this, `off` disables it). Tallpine checks running
passes every minute, so this happens even when nobody runs `tallpine review ls`. When a pass fails, or Tallpine cancels it because its
reviewer became unavailable, Tallpine tells the worker session directly and files an
`[auto] review_failed` report with the reviewer, head and reason to the
project orchestrator, once per pass. A pass you cancel, or one superseded by a
review of a newer commit, is not announced. `tallpine review ls` prints the reason
under the table. Decide whether to trigger again; a failed pass posted no
review.

**Examples:**

```bash
# Inside a worker: request a review of this session's PR once it is ready
tallpine review trigger
```

```bash
# Orchestrator: review a worker's PR with a specific reviewer
tallpine review trigger mer-3 --agent codex --model gpt-5.5
```

```bash
# Add a second opinion on the same commit while the first reviewer runs
tallpine review trigger --agent claude-code --rerun
```
