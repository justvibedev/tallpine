# tallpine backlog

A per-project list of deferred work, kept outside chat. It is not a task list
for the current turn: keep your harness task list for this turn's steps.

## Who does what

| Actor | May |
|---|---|
| Human | everything; items they add are `open` |
| Orchestrator | add (to the inbox, or `--open` when the human asked for it in this conversation), edit, set fields, link, hold, reorder, accept, done, drop, delete and purge closed items |
| Worker | add to the inbox, list, show, next. Workers file follow-ups through `tallpine report` (see [report.md](report.md)); they cannot edit, close, reorder or link |
| Reviewer | nothing |

States: `inbox` (filed by an agent, waiting for triage), `open` (accepted; drag
order is priority), `done`, `dropped`. A `hold` is free text that makes `next`
skip an item. Progress such as "in review" is derived, never stored.

## Syntax

```text
tallpine backlog add <title|-> [--note <md>] [--done-when <text>] [--scope local|pr|pr-merge]
                         [--parent B3] [--hold <why>] [--agent-notes <md>] [--open]
tallpine backlog ls [--state inbox|open|done|dropped|all] [--parent B3] [--held|--unheld] [--q <text>] [--limit N]
tallpine backlog show <ref>
tallpine backlog edit <ref> [--title ..] [--note ..] [--done-when ..] [--scope ..] [--parent B3 | --no-parent] [--agent-notes ..]
tallpine backlog set <ref> key=value...        # custom fields; key= clears one
tallpine backlog link <ref> <kind:value>       # also: unlink
tallpine backlog accept|reopen|done|drop <ref> [--reason <text>]   # --reason on done and drop
tallpine backlog delete <ref> [--yes]          # permanent; done/dropped only
tallpine backlog purge --done [--dropped] [--older-than 30d] [--dry-run] [--yes]
tallpine backlog hold <ref> <reason...>        # also: unhold <ref>
tallpine backlog move <ref> --before <ref> | --after <ref>
tallpine backlog next [--parent B3]
```

Every command takes `--project <id>` (default: `TALLPINE_PROJECT_ID`, the current
session, or the current registered repo) and `--json`.

Refs are per-project and monotonic: `B12` (also `b12` or `12`). A ref is never
reused, not even after a delete.

`done` and `drop` only archive. `delete` and `purge` remove closed items for
good (links go with them); open and inbox items are refused. Deleting a parent
deletes its closed children too, and is refused while any child is still open
(`purge` skips such a parent). Without `--yes` they ask first; `--dry-run`
lists what `purge` would remove. Only the human and the orchestrator may delete;
delete only when the human asked for the cleanup.

`add -` reads stdin: the first line is the title, the rest is the note. Use a
heredoc so backticks and quotes stay literal:

```bash
tallpine backlog add - --parent B3 <<'EOF2'
Allow --effort for deepseek-harness at spawn
Needs `tallpine spawn --effort`; see the harness adapter.
EOF2
```

## Rules

- **Hierarchy is one level.** A child cannot have children, a parent cannot be
  a child, and a parent cannot be started: `next` never returns one, because
  running a parent runs its children.
- **`blocked_by`** links order work: `tallpine backlog link B7 blocked-by:B5`. Cycles
  and cross-project links are refused. An item is blocked until every blocker is
  `done` or `dropped`.
- **Link kinds:** `session`, `pr`, `issue`, `artifact`, `memory`, `item`
  (a plain relation) and `blocked-by`. For `item` and `blocked-by` the value
  is another item's ref.
- **Custom fields** (`set`): keys match `[a-z0-9][a-z0-9._-]{0,39}`, values up
  to 500 characters, at most 32 per item.
- **`next`** returns the topmost `open` item that is unheld, has no children and
  has no blocker still `inbox` or `open`. It prints "nothing is ready" when none
  qualifies.
- An orchestrator only starts work on an item when the human asked for it. A
  held item is never started.

## Lifecycle (automatic)

- `tallpine spawn --backlog B12` links the worker's session to the item. The item must
  be `open`, unheld and without an open blocker (or `--force`), and not a parent. The item's title, note,
  done-when and agent notes become part of the worker's task.
- When **every** PR linked to the item, or opened by a linked session, is
  merged, the item becomes `done` ("merged #N"), whether the merge came from
  `tallpine pr merge` or GitHub. Items with scope `local` are never closed
  automatically.
- An item closes when **every PR linked so far** has merged. If one item will
  produce several PRs, split it into child items (one PR each): otherwise it
  closes as soon as the first PR merges, before the next PR is opened.
- A PR closed without merging, or a linked worker that ended without merging,
  leaves the item open and adds a line to its agent notes (the last 5 `- auto`
  lines are kept).
- A worker's `done` report closes only items that have no PR at all.
- When all of a parent's children are done or dropped, the parent becomes done
  ("children closed"). Reopening or adding a live child reopens a parent that
  was closed this way, never one closed by hand.
- Agent-filed inbox items nobody touched for 14 days are dropped ("expired");
  human-filed items never expire.
- `show` and `ls --json` carry `badges` (`active`, `inReview`, `landed`),
  derived from linked sessions and PRs.

## Run mode ("finish the backlog")

On by default; if the daemon runs with `TALLPINE_BACKLOG_RUN=off`, every `tallpine backlog run` call answers `BACKLOG_RUN_DISABLED`. A run is permission: you never start backlog items on your own, only inside a run the owner asked for ("finish the backlog", "finish B12", "do everything under B30", or the Run button, which sends you one message naming the run, `R<n>`).

```
tallpine backlog run start --scope pr --all            # or --parent B30, or B12 B14 ...
tallpine backlog run plan R3 --file plan.json          # {"lanes":[[{"ref":"B12","model":"opus"}],[{"ref":"B14","model":"sonnet"}]]}
tallpine backlog run status [R3]                       # lanes, per-item status, cost and elapsed
tallpine backlog run item R3 B12 --status active|in_review|needs_you|landed|held|failed|queued [--reason ..] [--question ..]
tallpine backlog run pause R3 --reason "usage limit" --resume-at 2026-10-28T15:00:00Z
tallpine backlog run resume R3 | stop R3
tallpine backlog run finish R3 --summary-file -        # your narrative; the page adds cost and elapsed time
```

Rules:
- **Scope first.** Ask the user once per run: `local` (reviewed commit in the worktree; you close the item), `pr` (green and reviewed PR; the item closes when they merge) or `pr-merge` (merged PR). An item's own scope overrides it. Chat requests make you create the run; the Run button already did.
- **Plan first, then run.** Split big items into one-PR children (`tallpine backlog add --parent`), order by `blocked_by`, put independent items that touch different code areas in separate lanes, pick a model per item by the routing policy. Save the plan with `run plan` and start at once unless the owner asked to see it. The daemon refuses a plan with a parent, a duplicate, an unknown ref, or a blocked item that is not after its blocker in the same lane.
- **You write the item status.** Nothing else moves it: write `active`, `in_review` (frees the worker slot), `landed`, and so on as things happen. The page shows an item as landed once the item itself is done, but the run only ends when every item's stored status is settled (landed, held or needs_you), so write `landed` when you see the merge.
- **Re-planning keeps work on record.** `run plan` may add, move or drop only items that are still `queued` with no failure; an item with a worker, a result or a failure must stay in the plan. `run item --status active` also waits for the item ahead of it in its lane and for its blockers to land.
- **Plans come first when planning is on.** With planning mode `always` the daemon starts a planner for each run item without an approved plan, at most as many as the worker cap; with `auto` it plans only items you already marked with `tallpine plan start <ref>` before the run; with `off` it plans nothing. A waiting item shows `needs_you` with reason `plan_pending` and the owner reviews its plan card; do not start it, do not overwrite that status, and do not ask the owner about it yourself. When its plan is approved it goes back to `queued` and you spawn it as usual. If the plan splits it into children, the daemon replaces the parent with its children in the run, in plan order and in the same lane; nothing to do. A failed plan stays `plan_pending` until someone plans it again (`tallpine plan start <ref>`). Every item follows its own plan; the run has no gate.
- **Same area, one at a time.** No stacked PRs; merge one per area at a time.
- **At most 3 workers at once.** `run item --status active` fails when 3 are active. Spawn with `tallpine spawn --backlog <ref>`, then set `active`; `in_review` while the PR is under review.
- **Review.** Risky changes get an independent Opus review; low-risk UI gets the native review. Fixes go back to the same worker. Run local CI before every merge.
- **Usage limit.** On the Claude 5-hour limit, `run pause`, let the in-flight workers settle, and `run resume` when it resets.
- **Needs you.** One specific question: `--status needs_you --question "..."`. Carry on with the other items; the run ends without it.
- **Fails twice, hold.** The second `failed` becomes `held` with your reason. Do not retry a third time.
- **Stop.** `stop` (yours or the owner's) means finish the work in flight and start nothing new. A UI Pause, Resume or Stop sends you one message; the state is already changed, so check `run status`.
- **Check state every turn.** While a run is open, start each turn with `tallpine backlog run status`; your own to-do list does not survive compaction, the run does.
- **End.** When every item has landed, is held, failed twice or waits on the owner, the run ends itself. Close it with `run finish` and a short summary: what landed, what waits on the owner, what is held. Cost and elapsed time per item come from the usage ledgers and show on the page; elapsed is wall-clock from the first session's start, not active time. Items landed by a run still close through the normal lifecycle above.
- One run is open per project. `status` with no argument shows it.

## JSON

`--json` prints the daemon response unchanged. `ls` returns
`{"items":[...]}` with per-kind `linkCounts`; `show`, `add`, `edit` and the state
commands return `{"item":{...}}` including `links`, `fields`, `origin` and
`agentNotes`; `next` returns `{"item":null}` when nothing is ready.

## Errors

Usage errors exit 2 and daemon errors exit 1. Codes include `BACKLOG_ITEM_NOT_FOUND`,
`BACKLOG_HIERARCHY`, `BACKLOG_CYCLE`, `BACKLOG_CROSS_PROJECT`,
`INVALID_BACKLOG_FIELD` and `AGENT_SCOPE_FORBIDDEN` (a worker tried to edit).
