# tallpine orchestrator

Manage orchestrator sessions.

## Syntax

```
tallpine orchestrator <subcommand> [flags]
```

## Subcommands

---

### tallpine orchestrator ls

List orchestrator sessions. Aliases: `ls`, `list`. An orchestrator whose project turned on `crossProjectOrchestration` also sees the orchestrators (never the workers) of other projects.

**Syntax:**
```
tallpine orchestrator ls [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--json` | Output as JSON | - |

---

### tallpine orchestrator new

Start the project's orchestrator, or return the running one; `--lane <ref>` starts the orchestrator that owns that top-level backlog item. `--prompt <text>` sends the new orchestrator its first message right after it starts.

An orchestrator can use this only on a project its own project created (`tallpine project add`), and only while its project's `crossProjectOrchestration` agent policy is on (Settings > Agent policy; off by default). Agents may not pass approval overrides. `--lane <ref>` (with `--prompt` as the lane's brief) and `orchestrator end --lane <ref>` are allowed only for a project's main (non-lane) orchestrator while the project's `orchestratorLanes` agent policy is on (off by default); a lane with a worker that has an open PR cannot be ended by an agent, and the lane records the starting orchestrator as its parent session.

**Syntax:**
```
tallpine orchestrator new [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--project` | Project id | - |
| `--lane` | Top-level backlog item ref (e.g. B12) this orchestrator owns | - |
| `--prompt` | First message to send to the orchestrator after it starts | - |
| `--json` | Output as JSON | - |

---

### tallpine orchestrator handoff

Replace this orchestrator with a fresh one for the same project. Write a state note first (exact SHAs, worker-to-PR mapping, pending reviews, open decisions) into your artifact directory. The daemon starts a new orchestrator seeded to read that note and continue, then retires this one (its history stays readable). Workers keep running and report to the new orchestrator. Run it when the user says to hand off; it only works on your own session.

**Syntax:**
```
tallpine orchestrator handoff [--note <path>] [flags]
```

**Flags:**

| Flag | Meaning | Default / Required |
|---|---|---|
| `--note` | State note file inside your artifact directory | Newest file in it |
| `--session` | Orchestrator session id | `$TALLPINE_SESSION_ID` |
| `--json` | Output as JSON | - |

## Examples

```bash
# Hand off to a fresh orchestrator, seeded from the note just written
tallpine orchestrator handoff --note <your artifact dir>/orchestrator-state.md
```

```bash
# List all orchestrator sessions
tallpine orchestrator ls
```

```bash
# List orchestrator sessions as JSON
tallpine orchestrator ls --json
```

## Durable intents

Owner intents and standing decisions belong in the daemon, not in chat history, so they survive compaction and handoff:

- `tallpine orchestrator intent add <text>` records one (e.g. "hold install until owner switches accounts", "merge #N after #M", "owner said X").
- `tallpine orchestrator intent ls` lists the open ones (`--all` includes cleared). Run it after a compaction or handoff.
- `tallpine orchestrator intent done <id>` clears one when it is resolved.

## Research mode

An orchestrator compacts early (120k) so coordination stays cheap. When you decide a task needs you to examine a large codebase or document set yourself (not a worker), raise the window for the duration of that analysis:

- `tallpine orchestrator research on` raises the compaction window (250k by default). Claude Code orchestrators only; it prints the window now in effect.
- `tallpine orchestrator research off` returns to the coordination window; run it when the analysis is done and record its conclusions with `tallpine orchestrator intent add`.
- `tallpine orchestrator research status` shows whether it is on.

It also ends when you are idle-compacted or handed off, and after an hour without a turn. The command refuses with a clear error when the owner turned "Dynamic orchestrator context" off in Settings or set the project's `orchestrator.researchCompactWindow` to `off`; do not work around that, delegate the reading to workers instead. Do not switch it on for plain coordination.
