---
name: using-tallpine
description: "Catalog of the Tallpine `tallpine` CLI: spawning workers, managing sessions and projects, creating reusable Cues, sending messages, controlling the shared browser, previewing pages, and daemon control. Use when using the tallpine CLI, creating Cues, spawning workers, or managing Tallpine sessions in a Tallpine workspace."
trigger: "Using the tallpine CLI in a Tallpine workspace: creating Cues, spawning workers, managing sessions/projects, sending messages, controlling or previewing pages."
---

# Tallpine CLI Catalog

`tallpine` is a thin CLI over the local Tallpine daemon. Every command is `tallpine <command> --help` for the authoritative flag list.

| Command | What it does | When to use | Details |
|---|---|---|---|
| `spawn` | Spawn a project worker, orchestrator, or projectless standalone worker | Starting a new task or issue | [commands/spawn.md](commands/spawn.md) |
| `session` | Manage agent sessions (list, kill, rename, restore, history, etc.) | Inspecting or controlling running/terminated sessions, or reading the full conversation after taking over from another agent | [commands/session.md](commands/session.md) |
| `agent` | Inspect installed harnesses and launch readiness | Choosing or diagnosing an agent harness | `tallpine agent ls` |
| `pr` | Merge a PR or resolve its review threads | Completing a reviewed change | `tallpine pr merge` / `tallpine pr resolve-comments` |
| `project` | Register, inspect, configure, or remove projects | Setting up or managing repos Tallpine knows about | [commands/project.md](commands/project.md) |
| `automation` | Manage durable recurring session automations | Creating, editing, disabling, deleting, and inspecting scheduled runs | [commands/automation.md](commands/automation.md) |
| `cue` | Create or list reusable project Cues | Saving a repetitive command or agent task at the user's request | [commands/cue.md](commands/cue.md) |
| `orchestrator` | List and start orchestrator sessions | Viewing which sessions are orchestrators | [commands/orchestrator.md](commands/orchestrator.md) |
| `review` | Start, list, cancel, or submit Tallpine's native reviewer for a worker's PR (default: this session) | Reviewing implementation PRs owned by Tallpine workers. Use a normal review-only worker for someone else's PR | [commands/review.md](commands/review.md) |
| `send` | Send a message to a running agent session | Messaging a live agent; use `--steer` to correct it mid-turn | [commands/send.md](commands/send.md) |
| `backlog` | File, triage, order and close a project's deferred work | Capturing ideas and follow-ups, picking the next item, tracking dependencies | [commands/backlog.md](commands/backlog.md) |
| `report` | Persist a meaningful worker report | Checkpoints, blockers, decisions, outputs, and completion | [commands/report.md](commands/report.md) |
| `preview` | Start a session-owned app or open an exact URL/file | Running and showing the worker's relevant app, Markdown, HTML, PDF, or image | [commands/preview.md](commands/preview.md) |
| `render` | Show a self-contained HTML page inline in a chat thread | Answering with a chart, table, diagram, or mockup | [commands/render.md](commands/render.md) |
| `browser` | Inspect and control the session's shared live browser | Verifying a web app through snapshots, interactions, waits, screenshots, console, and errors | [commands/browser.md](commands/browser.md) |
| `start` | Fetch (if needed) and open the Tallpine desktop app | Launching the app | [commands/start.md](commands/start.md) |
| `stop` | Stop the Tallpine daemon | Shutting down Tallpine | [commands/stop.md](commands/stop.md) |
| `status` | Show daemon status | Verifying the daemon is up and healthy | [commands/status.md](commands/status.md) |
| `doctor` | Run local health checks | Diagnosing Tallpine setup problems | [commands/doctor.md](commands/doctor.md) |
| `version` | Print version information | Checking installed version | - |
| `completion` | Generate shell completion scripts | Setting up tab completion | - |

## Conventions

- Most read commands accept `--json` for machine-readable output.
- `-p / --project` scopes session subcommand lookups to one project.
- Session and project ids are shown by `tallpine session ls` and `tallpine project ls`.
- To refer to a session in Tallpine Chat or the Tallpine terminal, use the canonical in-app
  link `tallpine://sessions/{project-id}/{session-id}`. Read
  [commands/session.md](commands/session.md) for identity, encoding, and safety
  rules before constructing one.
- When the user asks to save a repeatable task as a Cue, use `tallpine cue list --json` to check existing definitions, then `tallpine cue create`. An agent Cue stores a reusable instruction; a command Cue stores an exact shell command. This agent-facing command can create and read Cues, but cannot edit or delete them. Direct the user to project settings for changes to existing Cues.
- Inside a session, `tallpine` authenticates with the session's own scoped token:
  workers act on their own session, orchestrators on their own project.
  Approvals, settings, project configuration, automations, daemon control,
  and other projects are the user's; the one config change an orchestrator may
  make is its own project's rules text (`tallpine project set-config <own-id>
  --orchestrator-rules ... --agent-rules ...`, nothing else in the same call).
  A `403 AGENT_SCOPE_FORBIDDEN` means ask
  the user to do it in the app; never work around it. Exception, off by
  default: when the project's `crossProjectOrchestration` agent policy is on,
  an orchestrator may list and message other projects' orchestrators
  (`tallpine orchestrator ls`, `tallpine send`), register a project
  (`tallpine project add`), and configure it and start its orchestrator
  (`tallpine project set-config`, `tallpine orchestrator new --prompt`) only
  for projects its own project created. A refusal that names the policy means
  it is off.
- `--agent` is an alias for `--harness` on `tallpine spawn`.
- Every command accepts `-h / --help` for the full flag list.
- For frontend launch, preview selection, or artifact handoff, read
  [commands/preview.md](commands/preview.md) before acting. Its static-file,
  project-runtime, and automatic-handoff rules are load-bearing.
- For page inspection, interaction, or request diagnosis, read
  [commands/browser.md](commands/browser.md). It defines shared-tab behavior
  and the opt-in network policy.

Use [references.md](references.md) only when a natural-language request does
not map clearly to a command above.
