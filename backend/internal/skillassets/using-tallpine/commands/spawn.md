# tallpine spawn

Spawn a worker or orchestrator agent session in a registered project, or a standalone worker session that is not tied to a project.
Standalone sessions run in a Tallpine-managed directory. Register a project first with `tallpine project add` for project-scoped sessions.

## Syntax

```
tallpine spawn [flags]
```

## Flags

| Flag | Meaning | Default / Required |
|---|---|---|
| `--branch string` | Branch for the session worktree | `tp/<session-id>/root`, renamed to `tp/<task-slug>/root` after task naming |
| `--claim-pr string` | Claim an existing PR for authorized implementation continuation. Omit for review-only work | - |
| `--kind string` | Session role: `worker` or `orchestrator` | `worker` |
| `--harness string` | Agent harness to use (see list below) | Project `worker.agent`; required if the project has none |
| `--issue string` | Issue id to associate with the session | - |
| `--backlog string` | Backlog item (e.g. `B12`) to work on: its title, note, done-when and agent notes join the worker's task and the session is linked to the item. The item must be open and not a parent; held items need `--force`. Open blockers print a warning | - |
| `--force` | With `--backlog`, start a held item anyway | `false` |
| `--model string` | Agent model override for this session only; overrides project/role config | - |
| `--effort string` | Reasoning effort for this session only. Accepted by `codex`, `claude-code` and `deepseek-harness` (off, low, medium, high, xhigh, max; deepseek-harness maps them to off/low/high/max); other harnesses reject it | - |
| `--name string` | Display name shown in the sidebar (max 100 characters) | Required |
| `--mode chat\|tui` | Initial session interface; Chat requires harness support | Daemon default, otherwise Terminal UI |
| `--output artifact` | Ask the worker for a file in its artifact directory instead of a PR | Ordinary task |
| `--no-takeover` | Refuse if another active session owns the claimed PR (requires `--claim-pr`) | - |
| `--project string` | Project id to spawn the session in | Optional when `--standalone` is used; defaults to `TALLPINE_PROJECT_ID` or the current repo's registered project |
| `--standalone` | Spawn a projectless worker session in a Tallpine-managed directory | Disabled when `--project` is set |
| `--prompt string` | Initial prompt for the agent | - |
| `--skip-agent-check` | Skip CLI readiness warnings; the daemon still validates launch readiness | - |
| `--tracker-provider string` | Issue tracker provider: `github` or `gitlab` | `github` |

`--agent` is an alias for `--harness`.

Available harnesses: `claude-code`, `codex`, `kiro`, `deepseek-harness`. Check `tallpine agent ls` for the catalog on the installed build (workers get the daemon's cached catalog; only an orchestrator or human can force `--refresh`). Kiro runs in Terminal UI only. A project or session that still names a harness that was removed (for example `aider` or `opencode`) falls back to `claude-code` for new sessions, and an old session under such a harness is read-only.

`fx` is experimental and Terminal UI only: spawn it with `--agent fx --mode tui`.

`command-code` is Terminal UI only: spawn it with `--agent command-code --mode tui`.

## Examples

```bash
# Spawn a worker for issue 142 in the my-app project
tallpine spawn --project my-app --issue 142 --name "fix-session-leak" --prompt "Fix the session leak described in issue 142. Branch off upstream/main."
```

```bash
# Spawn a worker to continue implementation on an existing PR
tallpine spawn --project my-app --name "continue-pr-88" --claim-pr 88 --prompt "Continue implementation on PR 88."
```

```bash
# Spawn a normal worker to review someone else's PR without taking ownership
tallpine spawn --project my-app --name "review-pr-88" --prompt "Review PR 88 and submit only the requested provider review. Do not claim or attach the PR."
```

```bash
# Spawn an orchestrator using the project's orchestrator configuration
tallpine spawn --project my-app --kind orchestrator --name "coordinate-fix" \
  --prompt "Coordinate the fix and report the worker PR."
```

```bash
# Spawn a standalone worker whose deliverable is an artifact file, not a PR
tallpine spawn --standalone --agent claude-code --output artifact --name "q3-report" \
  --prompt "Write a one-page summary of Q3 incidents."
```

```bash
# Associate a GitLab issue with a worker
tallpine spawn --project my-gitlab-app --issue 42 --tracker-provider gitlab \
  --name "fix-issue-42" --prompt "Fix GitLab issue 42."
```
