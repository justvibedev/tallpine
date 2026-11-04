# golden-eval

Replay harness to compare plan-in-place (`tallpine spawn --backlog <ref> --plan in-place`, project setting `planInPlace`) against a separate planner on merged backlog items (B335). Reusable for B336.

Each golden item replays in its own registered eval project `eval-<ref>` (a pruned base-only repo, see Live runs) so eval items and plans stay out of the real backlog. Nothing here pushes.

## Files

- `golden.json`: `{version, note, items: [{ref, kind: "single"|"multi", baseCommit, prs, title, note, doneWhen, answers: [{question, answer}], decisions, decisionsSource}]}`. 10 items: 6 single-PR, 4 multi-PR (B88, B100, B161, B166). Only B310 has real owner answers; for the others the proxy takes the recommended option. `prs` are derived from commit subjects (done items carry no PR links). `answers` are the real owner answers the proxy replays; `decisions` are the recorded decisions a blind review checks the first head against.
- `plan.mjs`: the pure helpers (`planRuns`, `armCommands`, `buildSchedule`, `renderSchedule`, `matchAnswers`); no I/O, imported by every other module. `run.mjs` is only the CLI entry and nothing may import it (it awaits `cli.mjs`, so an import back is a cycle that exits silently); `run-cli.test.mjs` runs it as a subprocess.
- `run.mjs`: `--arm in-place|separate --item <ref> --rep <n> [--dry-run]`. `--dry-run` prints the spawn / answer / approve schedule (70 min proxy delays, at most 2 concurrent runs) without touching a daemon. The helpers in `plan.mjs` include `armCommands` (the `tallpine` calls per arm, in two phases: `plan` first, `build(childRefs)` only after the proxy approved, because a split plan makes the parent unspawnable) and `matchAnswers` (golden answers to a plan's questions). `--live` executes those calls (see Live runs).
- `collect.mjs`: builds one JSON result row from `tallpine session usage --json` (planner and implementer), `tallpine plan show --json`, the driver event log `[{at, type: spawned|questions|answered|plan|approved|head}]`, and for Claude Code a transcript JSONL (tool calls and cache-read tokens before the first commit; `n/a` for other harnesses).
- `report.mjs`: `rows.jsonl [prefix] [seed]` writes `<prefix>-report.md` (mean, min-max, sd per arm), `<prefix>-grading.md` (question sets under anonymous ids, shuffled) and `<prefix>-grading-key.json` (ids back to arm; keep it from the grader).

Wall time to first head runs from the `spawned` event to the `head` event; `spawned` is each arm's first step (the in-place spawn, the separate arm's `plan start`), so planning time counts in both arms. The dry-run start times are lower-bound estimates.

Tests: `node --test scripts/golden-eval/`.

## Live runs

The orchestrator drives setup and runs; no owner step. Setup uses only what an orchestrator may do on projects it registered itself: `project add --path`, then `project set-config` with fields a creating orchestrator may set (`planInPlace`, `worker`, `planner`, `defaultBranch`; PR #769). Answering and approving plans is allowed for the orchestrator (`backlog/plan.go` `decider`); a worker session fails at the first plan command. A managed `policy.planner.inPlace` lock still wins at spawn.

```
node scripts/golden-eval/run.mjs --setup --eval-root <dir> --worker-model <m> --planner-model <m> [--item B310] [--arm in-place|separate] [--rep 1]
node scripts/golden-eval/run.mjs --live  --eval-root <dir> --out <dir> [same filters]
```

- `--setup` builds one pruned base per item at `<eval-root>/base-<ref>` (no remote, one `main` at the base commit, later objects gc'd so the real solution is unreadable), then one local clone of it per run (item x arm x rep) at `<eval-root>/<ref>-<arm>-r<rep>` (hardlinked objects: one pruned store per item on disk), and registers `eval-<ref>-<arm>-r<rep>` marked `eval: true` (what lets the orchestrator that created it drive its backlog, plans and sessions and approve plans without an owner chat message; real projects keep that guard) with `planInPlace: true` and the role models. Rerunning `--setup` on already registered projects only updates their config, so it adds the mark. One project and clone per run means no run can read another arm's or an earlier attempt's branches.
- `--live` runs at most 2 runs at once. The driver is the owner proxy: it answers 70 min after the questions event and approves 70 min after the plan event. The first head is the first commit on the implementer's branch ahead of the base. A run's sessions are ended with `tallpine session kill` once its row is written or it ends for good (plan failed or superseded, implementer ended, deadline), so nothing builds past its slot; a failed poll is retried 5 times and a transient error leaves the sessions alive for a resume. The eval item is filed with `--open` (spawn and plan start refuse inbox items), and a resumed run that finds its item still in the inbox accepts it instead of adding a duplicate. Runs are resumable (state and events in `<out>/<id>/`): a plan start that failed is retried, a finished run keeps its `row.json` (no usage re-fetch) and `rows.jsonl` is rebuilt from every `<out>/*/row.json` (a filtered resume keeps the other rows). A failed run goes to `failures.jsonl` and does not stop the others. Eval items use scope local, and commands pass a guard that refuses `gh`, `git push`, `tallpine review|merge|pr` and any non-`eval-*` project.
- Not automated: noting plans that contradict recorded decisions; transcript lookup is best-effort (`n/a` if not found); findings on the first head still come from an external blind review.
