# 8. Scoped session tokens for agents

Date: 2026-10-26
Status: Accepted

## Context

ADR 0007 put the loopback listener behind a per-install token. Every agent
the daemon launches still ran its `tallpine` calls (hooks, `tallpine report`, `tallpine send`,
`tallpine spawn`) with that token. So any agent could do what the desktop app can:
spawn a `bypass-permissions` session, type into or read any other session in
any project, resolve its own pending approval, change project config or
global settings, open a shell terminal, or stop the daemon. With 15 projects
and up to 30 live agents on one machine, a single prompt-injected agent could
reach all of them.

## Decision

Every agent launch gets a scoped credential in `TALLPINE_SESSION_TOKEN`. The `tallpine`
CLI sends it in the same `X-VO-Token` header and, when it is set, never reads
`daemon.token`. The daemon authorizes it against a deny-by-default capability
policy. The desktop app, the user's own shell, and the LAN listener keep the
install token or bearer auth and full access.

### The token

- **Shape.** `vos1.<session-id>.<mac>` for a worker or orchestrator session.
  `vor1.<review-id>.<mac>` for a reviewer. The MAC is HMAC-SHA256 under a key
  derived from the install token (`internal/sessiontoken`).
- **Bound to the session.** A session token MACs the session id and the
  session's browser-capability verifier. Session Manager already rotates and
  persists that verifier on every agent launch (spawn, restore, resume,
  interface and account transitions), before the agent starts. Each launch
  therefore gets a new token, and the previous launch's token stops verifying.
  A reviewer token MACs its review id and the worker under review.
- **Revocable.** A token is refused once its session is terminated, when the
  session relaunches, or when the reviewed worker is terminated. Replacing
  `session.key` revokes every scoped token at once.
- **Stateless.** Nothing new is stored. Agents in tmux and persistent chat
  hosts outlive daemon restarts, so their tokens must survive a restart too.
  They do: the key comes from `session.key`, which persists while any session
  is live (an existing install seeds it from its current `daemon.token`, so
  live tokens stay valid), and the verifier is persisted. When the daemon
  starts with no live session, no agent holds a token, so `session.key` is
  rotated; a live session keeps the key so its agent stays valid. The
  install token itself is rotated on every daemon start; deleting
  `session.key` revokes every scoped token.
- **Bound to its daemon.** The launch also exports
  `TALLPINE_SESSION_TOKEN_RUN_FILE`, the run file of the issuing daemon. The CLI
  sends the scoped token only when the daemon it is calling resolves to that
  run file. A call to any other daemon, such as an isolated dev or lab daemon
  with its own `TALLPINE_RUN_FILE`, uses that daemon's install token as before.
- **Injected, not inherited.** Only the launch path mints one. The base
  runtime env strips any `TALLPINE_SESSION_TOKEN` that project env supplies. A
  daemon started from inside an agent scrubs it, like `TALLPINE_SESSION_ID`.

### Capabilities

`agentRules` in `backend/internal/httpd/agent_scope.go` is the complete
allowlist. Everything else is refused with `403 AGENT_SCOPE_FORBIDDEN`. That
includes `/mux`, `/shutdown`, pprof, `/api/v1/events`, settings, project
config and permissions, mobile, shell terminals, agent and account
management, GitHub credentials, link previews, automations, Cue invocation and
editing, and every approval or input resolution, even on the agent's own
session.

| Role | May |
|---|---|
| Worker | Report for itself; post its own activity hooks; read its own session, workspace, PR, conversation, and reviews; claim a PR for itself, resolve review threads on its own PR, and trigger or cancel its own review; drive its own preview, browser, renders, and artifacts; list sessions (it sees only itself and its project's orchestrators); message its own project's orchestrator; create and list its project's Cues (never invoke or edit them); read its project's backlog and file items into its inbox (the server forces `inbox` and takes the origin from the token; it cannot edit, close, reorder or link items). |
| Orchestrator | Everything a worker may do for itself, plus, within its own project only: list and read sessions, spawn workers, message, kill, restore, exit, resume, rename, and roll back sessions, claim PRs for them, resolve review threads on and merge their PRs, trigger and cancel their reviews, clean up the project, hand off (replace) its own orchestrator session, and read the project's reports, and manage the project's backlog (edit, accept, close, reorder, link, set fields). |
| Reviewer | Post its own review's activity; read the worker under review and its reviews and PR; submit that worker's review. |

Route-level scope is checked in the middleware against the path's session,
project, or review id. Request bodies and queries are checked in the
handlers, with the principal that `requestscope.AgentFrom` puts on the
request context:

- An orchestrator's spawn must be a worker in its own project, with itself
  as parent. It may not set `approvalMode`, so the worker inherits the
  orchestrator's policy.
- `send` and `steer-or-send` always record the agent as the sender and never
  as `userAuthored`.
- Reports are for the caller's own session. Report listing and cleanup are
  limited to its own project.
- An orchestrator hands off only its own session
  (`POST /orchestrators/{id}/handoff`, rule target self): never a worker's or
  another orchestrator's. The note must sit in the retiring orchestrator's
  artifact directory, and workers are never touched.
- Session, orchestrator, and project lists are filtered to what the role may
  read.
- A review that an agent triggers is recorded as `agent`, never `manual`.
- A reviewer's `reviews/submit` is bound to its own review: every run in the
  submission must belong to it, checked before any result is recorded, so a
  second reviewer on the same worker cannot record verdicts for the other's
  runs.
- PR merge and thread resolution look up the PR's owning session the same
  way the action resolves the PR it mutates (merge by URL, resolve by number).
- A PR claim checks the PR's current owner, terminated owners included, inside
  the claim transaction (`ports.GuardedPRClaimer`). A worker may claim only a
  PR no other session owns. An orchestrator may move a PR only between
  sessions of its own project. So a worker cannot take a sibling's PR to gain
  its review-thread rights, and an orchestrator cannot pull a PR out of
  another project that shares the repository. Continuing a terminated
  session's PR in a new worker is the orchestrator's job
  (`tallpine spawn --claim-pr`, `tallpine session claim-pr <worker> <pr>`).

### Audit

Each refusal writes a `WARN` record to the daemon log, with
`audit=agent_scope`, the outcome (`unauthenticated` or `forbidden`), the
reason, the method, the path, the remote address, and the principal. When the
token was refused, the record holds the claimed id. Tokens are never logged.

## Consequences

- Agents that call the daemon through `tallpine`, hooks, or the reviewer launcher
  are confined to their session or project. Approvals, settings, and daemon
  control need the user.
- Sessions launched before this change keep running without
  `TALLPINE_SESSION_TOKEN`, and their CLI falls back to the install token until
  they are next launched.
- **Same-user limit.** Agents run as the user. An agent that reads
  `~/.tallpine/daemon.token` directly (for example with `cat`) still gets full
  authority. The file's path is not secret either, since it sits next to
  `TALLPINE_RUN_FILE`. Scoped tokens close the default path: the token is not in
  the agent's env, the CLI does not read the file when a session token is
  present, and the `tallpine` commands the agent is told to use cannot escalate.
  Only OS-level isolation closes the rest: a harness sandbox that denies
  reads of `~/.tallpine`, or running agents as another user.
- **Tool server.** The `tallpine mcp` tool server that chat harnesses start gets an
  explicit env that is fixed before the launch credential exists, and
  persistent chat hosts reconnect without a new env. So it still uses the
  install token. Its tool surface is fixed to `html_preview` and
  `html_render` for its own `TALLPINE_SESSION_ID`, so it gives the agent no extra
  authority.
- **Side chats** inherit the main session's token and act as that session.
- `TALLPINE_DAEMON_AUTH=off` plus `TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1` disables scoped
  enforcement along with the install token check, in an unstamped dev build
  only; release builds and `off` alone are ignored with a warning.

## Opt-in cross-project orchestration

The `crossProjectOrchestration` agent policy (off by default; enabled per
project by the user or MDM) adds capabilities that would otherwise be denied.
It is also turned on automatically for each project an orchestrator registers
(`afterAgentCreatedProject`), so the capability is transitive: a created
project's orchestrator can register further projects. A managed-off lock keeps
the new project's setting off and stops the chain. Limits are enforced in
`httpd/agent_scope.go` and the project service (`service/project/agent_create.go`),
with tests in `cross_project_test.go` and `agent_create_test.go`:

- **Orchestrators only.** Workers and reviewers never get them.
- **Other projects' orchestrators only.** An orchestrator may spawn, message,
  and read the orchestrators of other projects, never their workers.
- **Created-project ownership.** An orchestrator may register a project and then
  change that project's config. Creator rows record ownership; it cannot touch
  projects a person registered, and cannot adopt or revive any existing id.
- **Eval projects.** A created project the creating orchestrator marks
  `eval` (config `eval: true`; only the user or that orchestrator may set it) is
  one the orchestrator may also drive from its own project, and only when
  crossProjectOrchestration is on: the project's backlog (list, add, show, state,
  plan start/show/answers/defaults/approve), spawn of workers in it, and session
  get/list/send/usage/kill for its sessions. The gate requires both "created by
  the caller" and "marked eval" (`requestscope.Agent.EvalAccess`); workers and
  reviewers never match, and every other route stays refused. On such a project
  the orchestrator approves plans without the owner's chat message (it is the
  owner's proxy); real projects keep that guard, and a managed
  `planner.autoApprove` of `off` removes the waiver everywhere.
- **Config allowlist and permission cap.** Only `defaultBranch`, `sessionPrefix`,
  `agentRules`, `orchestratorRules`, `noCI`, `planInPlace`, and per-role
  agent, model, effort, and permissions may be set. A role's permission mode may not exceed the mode
  the creator project's own session would launch with
  (`session_manager.EffectivePermissions`).
- **Path rules.** A new project may not be inside the Tallpine data dir, nor
  equal to, inside, or containing any registered project (any state), after
  symlink resolution.
- **Initial brief is trusted, later messages are data.** `POST /api/v1/orchestrators`
  accepts a `prompt` (`tallpine orchestrator new --prompt`). It rides the spawn,
  so the daemon delivers it as the new orchestrator's unframed first user turn
  once the chat controller is live (no send to a still-starting session, no 500),
  exactly like a worker's spawn prompt. An agent may use it only through the
  creator check above, so it comes from the orchestrator whose project created
  the target. If a running orchestrator is returned instead, nothing is
  delivered and the CLI falls back to a normal `send`, which is framed as
  untrusted data like every later cross-project message. Delivery logs
  `outcome=orchestrator_brief_delivered` with `creator_session`.
- **Audit.** Allowed cross-project requests log a daemon `WARN` line
  (`audit=agent_scope`, `outcome=cross_project_allowed`, with action, role,
  session, project); creates also log `outcome=project_created`. See the Audit
  section above.

## Follow-ups

- Bind the launch credential into the `tallpine mcp` env in the ACP and Codex chat
  drivers.
- Rotate reviewer tokens per review pass, not only per review.
- Preview capability tokens for browser tabs, so that unauthenticated preview
  file reads (ADR 0007) are also bound to a session.
- Optional harness sandbox rules that deny reads of the install token file.
