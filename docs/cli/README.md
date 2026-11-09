# Tallpine CLI

The `tallpine` CLI is a thin Go/Cobra client for the local Tallpine daemon.
It opens the desktop app and discovers, inspects, or stops its daemon through the
loopback HTTP surface and the `running.json` handshake. It must not open SQLite
directly or call runtime, workspace, tracker, or agent adapters in-process.

When using the CLI directly from a shell, make sure the daemon is running first
by opening the desktop app or running `tallpine daemon` under a service manager. Product commands such as
`tallpine agent ls` and `tallpine spawn` call the loopback daemon and will fail with a
"daemon is not running" error if no `running.json` points at a live process. From
a source checkout, build and run the local binary explicitly, for example:

```bash
cd backend
go build -o ./bin/tallpine ./cmd/tallpine
./bin/tallpine agent ls
```

## Current commands

Every product command resolves to a daemon HTTP route. Run `tallpine <command>
--help` for the authoritative flag shape.

### Daemon control

| Command                       | Purpose                                                                                                                           |
| ----------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `tallpine start`                    | Open the desktop application, downloading a release if necessary; the app supervises its daemon.                                                                        |
| `tallpine stop`                     | Gracefully stop the daemon via loopback `POST /shutdown` after verifying daemon identity. `--stop-agents` stops every agent first. |
| `tallpine status` / `--json`        | Report daemon state from `running.json`, process liveness, `/healthz`, and `/readyz`.                                             |
| `tallpine doctor` / `--json`        | Check config, data directory, DB-file presence, daemon state, `git`, and (on Darwin/Linux) `tmux`; on Windows conpty is built in. |
| `tallpine completion <shell>`       | Generate completions for `bash`, `zsh`, `fish`, or `powershell`.                                                                  |
| `tallpine version` / `tallpine --version` | Print build metadata.                                                                                                             |
| `tallpine daemon`                   | Run the daemon in the foreground (normally supervised by the desktop app or an OS service manager).                               |
| `tallpine remote-host status/enable/disable` | Inspect or toggle this machine's authenticated remote listener through the local daemon. |

For a self-hosted machine, use the [host setup command](../self-hosted-remote.md)
to install the daemon with its Claude Chat runtime and start the OS user
service. It calls `tallpine remote-host enable` and prints the host ID, address, and
access key for **Settings → Connections → Other computers** on another desktop. With
`cloudflared` installed, setup `--tunnel` calls `tallpine remote-host enable
--tunnel-only`: its authenticated listener binds only to loopback. Direct
`tallpine remote-host enable --tunnel` keeps both LAN and Cloudflare access for users
who deliberately want both. Check `tallpine remote-host status` when the HTTPS
address is ready.
Running `enable` again prints the current details without rotating the
access key. `status` shows the access key from the host's local shell. The LAN
listener is HTTPS with a pinned certificate; do not publish its port directly to the internet.
Cloudflare terminates tunnel TLS and can see its traffic; quick-tunnel
addresses change on restart.

### Product commands

| Command                             | Daemon route                                   |
| ----------------------------------- | ---------------------------------------------- |
| `tallpine project add`                    | `POST /api/v1/projects`                        |
| `tallpine project ls`                     | `GET /api/v1/projects`                         |
| `tallpine project get <id>`               | `GET /api/v1/projects/{id}`                    |
| `tallpine project set-config <id>`        | `PUT /api/v1/projects/{id}/config`             |
| `tallpine project set-config <id> --github-user <login>` | With no other field flag: `PATCH /api/v1/settings/values` (`project.githubUser`; the login must be signed in to `gh`, an empty value returns to auto-pick; reaches only sessions started afterwards) |
| `tallpine github accounts [--project <id>]` | `GET /api/v1/github/accounts`, or `GET /api/v1/projects/{id}/github-accounts` with a project (logins only, push access to the project's repo, the pinned and auto-picked account; never tokens) |
| `tallpine project rm <id>`                | `DELETE /api/v1/projects/{id}`                 |
| `tallpine import ao`                      | `POST /api/v1/imports/ao` (read-only snapshot of the official `~/.tallpine` database; `--dry-run` plans without writing) |
| `tallpine agent ls`                       | `POST /api/v1/agents/readiness/ensure` (`display`) |
| `tallpine agent ls --refresh`             | `POST /api/v1/agents/refresh` (forced checks) |
| `tallpine automation create/list/get/update/delete/runs` | `POST/GET/PATCH/DELETE /api/v1/automations` |
| `tallpine cue create/list`                | `POST/GET /api/v1/projects/{id}/cues`         |
| `tallpine spawn`                          | Targeted launch ensure, then `POST /api/v1/sessions` |
| `tallpine session ls`                     | `GET /api/v1/sessions` plus per-session PR summaries; shows branch, PR, CI, review, unresolved threads, activity, and age. |
| `tallpine session get <id>`               | `GET /api/v1/sessions/{id}`; also prints the latest turn's final assistant message (`lastAssistantMessage`, detail read only, 4 KiB cap). |
| `tallpine session kill <id>`              | `POST /api/v1/sessions/{id}/kill`              |
| `tallpine session restore <id>`           | `POST /api/v1/sessions/{id}/restore`           |
| `tallpine session exit-agent <id>`        | `POST /api/v1/sessions/{id}/exit-agent`        |
| `tallpine session resume-agent <id>`      | `POST /api/v1/sessions/{id}/resume-agent`      |
| `tallpine session rename <id> <name>`     | `PATCH /api/v1/sessions/{id}`                  |
| `tallpine session cleanup`                | `POST /api/v1/sessions/cleanup`                |
| `tallpine cleanup [--dry-run]`            | `POST /api/v1/cleanup/runs`                    |
| `tallpine cleanup status`                 | `GET /api/v1/cleanup`                          |
| `tallpine cleanup history`                | `GET /api/v1/cleanup/runs`                     |
| `tallpine cleanup policy [--set k=v]`     | `GET /api/v1/cleanup`, `PATCH /api/v1/cleanup/policy` |
| `tallpine session claim-pr [<id>] <pr-ref>` | `POST /api/v1/sessions/{id}/pr/claim`        |
| `tallpine orchestrator ls`                | `GET /api/v1/orchestrators`                    |
| `tallpine orchestrator handoff [--note p]` | `POST /api/v1/orchestrators/{id}/handoff`     |
| `tallpine send`                           | `POST /api/v1/sessions/{id}/send`              |
| `tallpine report ...`                     | `POST /api/v1/reports`                         |
| `tallpine preview [url]`                  | `POST /api/v1/sessions/{id}/preview`           |
| `tallpine preview start/status/stop`      | `POST/GET/DELETE /api/v1/sessions/{id}/preview/server` |
| `tallpine browser ...`                    | `GET /api/v1/browser/status`, `POST /api/v1/browser/commands` |
| `tallpine hooks <agent> <event>`          | `POST /api/v1/sessions/{id}/activity` (hidden) |

`tallpine agent ls` asks the daemon to ensure display readiness, then prints the
existing table or legacy JSON projection. The daemon alone decides whether a
native check is needed. `--refresh` is a deprecated compatibility flag that
forces fresh installation and authentication checks before printing.
Workers are refused those routes (orchestrator-only), so for a worker `tallpine agent ls`
falls back to read-only `GET /api/v1/agents`, prints a note on stderr, and exits 0;
`tallpine spawn` skips its readiness preflight with a warning and lets the daemon validate.

`tallpine spawn` resolves project context in this order: explicit `--project`,
`TALLPINE_PROJECT_ID`, `TALLPINE_SESSION_ID` (by fetching the current session from the
daemon), then the current working directory matched against registered project
paths. If `TALLPINE_SESSION_ID` is set but the session cannot be fetched, pass
`--project` explicitly. Use `tallpine spawn --standalone --agent <agent> --name
<name>` to launch a worker in a Tallpine-managed plain directory without resolving
or registering a project. Standalone sessions do not support orchestrator,
branch, issue, or PR-claim options.

`tallpine session claim-pr <pr-ref>` attaches a PR to the current worker by reading
`TALLPINE_SESSION_ID`. From an orchestrator or external shell, pass the target
explicitly with `tallpine session claim-pr <session-id> <pr-ref>`. The explicit form
remains supported for backward compatibility and cross-session coordination.

Both `tallpine session claim-pr` and `tallpine spawn --claim-pr` claim ownership metadata
only. The claim step does not check out a branch or change HEAD, so
`branchChanged: false` renders as `checkout: not performed; workspace unchanged`
without asserting that HEAD matches the provider PR. `tallpine session claim-pr --json`
preserves that field; `spawn` has no JSON mode. Verify the branch and HEAD before
editing or pushing. Automatic checkout is deferred until exact-head verification,
takeover, concurrent provider changes, and worktree preservation can be handled
together.

`tallpine report` persists a worker-originated coordination claim before reporting
success. Free-form messages, reports with outputs (including checkpoints),
`--done`, `--stuck`, and `--needs-input` reports are immediately eligible for
delivery without an extra flag. A `--checkpoint` with no output does not wake an
idle orchestrator by itself: it rides the next delivered batch or the
orchestrator's next turn, or is delivered after at most ten minutes. An idle
orchestrator wakes for immediate reports; a busy Chat orchestrator retains reports in
the durable outbox until it can admit them, without forced concurrent execution.
The five-minute done settlement grace only governs missing-PR soft verification;
it never delays delivery. Startup recovery removes old initial batching delays
from unattempted reports while preserving retry backoff. `--stuck` requests
an interrupt at most once per worker every three minutes and coalesces equal
repeats. Reports route to the active project orchestrator at each attempt and
remain pending when none is active. Tallpine never spawns an orchestrator to deliver
a report. A project archived with `tallpine project rm` has no orchestrator left, so
its pending reports, including `[auto] exited` reports from the workers the
removal stopped, are settled undelivered instead of waiting forever. Each keeps
its reason, shown as `undelivered` in `GET /api/v1/reports`, and the daemon logs
the dropped report ids.

The daemon also files two system-generated free-form reports for worker
sessions (never orchestrators) from the turn facts it already receives (agent
hooks in Terminal UI mode, the controller's turn lifecycle in Chat mode):
`[auto] turn_ended` when a turn's end is followed by 60 seconds
(`turnnotify.DefaultIdleDelay`) of idleness, and `[auto] exited` when the agent
session ends (`SessionEnd`, or the Chat provider stopping; never a daemon
shutdown). Each carries the branch and a
truncated last-assistant-message excerpt, is due immediately, and is skipped
when the worker filed its own report in that turn. At most one is sent per
worker turn. State is in memory, so a daemon restart forgets pending timers.
A third, `[auto] start_failed`, is filed when a Chat worker's background start
gives up after `tallpine spawn` already answered (the ACP `session/new` handshake is
retried twice on a fresh process first). It carries the start error and the
`tallpine session resume-agent <id>` retry command, and the session reads `exited`
instead of `idle` until it is retried.
A fourth, `[auto] review_failed`, is filed when a Tallpine review pass of the
worker's PR fails or is cancelled by Tallpine because its reviewer became
unavailable. It names the PR, head, reviewer and reason, and the worker is
nudged with the same facts. The daemon's review sweep checks running passes
every minute (fails passes whose reviewer exited, sat idle at its prompt past
`TALLPINE_REVIEW_IDLE_GRACE`, default `10m`, minimum `1m`, `off` disables, or
exceeded `TALLPINE_REVIEW_PASS_DEADLINE`, default `45m`, minimum `10m`, `off`
disables) and announces each ended pass once, recorded durably on the run.
The report is filed first under an ID derived from the run, so a report that
fails to file is retried on the next sweep (for up to a day) without ever
being filed twice; the worker is nudged once the run is recorded as announced.
The deadline is applied without the worker lock or Chat recovery, so a launch
or restore that hangs cannot keep its pass running; the rest of the reconcile
skips a worker whose trigger or restore is still in progress. The notice does
not count as the worker reporting.

A worker that stays idle and silent after its turn is also nudged directly, so
one that ended its turn to wait on its own background build or test is woken
when that job finishes. The check runs `TALLPINE_WORKER_IDLE_NUDGE` (default `10m`,
`off` disables) after the turn ends, only while the session is `idle` and no
report was filed in that turn. While a background job is still running in the
worker's process tree (a shell started below the agent, in its terminal panes
or Chat provider host), the check repeats every 30 seconds for up to three
hours and nudges once the job exits. The nudge goes through the same guard as
other Tallpine nudges, so a session waiting on its user is never prompted. At most
one nudge is sent until the worker reports or receives other work.

Scheduled delivery and exact same-turn user-message piggyback use Chat's
durable semantic message boundary. Supported TUIs acknowledge delivery only
after their native prompt hook reports acceptance of the exact durable batch
identity. A successful terminal write is never acknowledgement. Reports remain
pending for TUI adapters that cannot expose this semantic boundary.

`GET /api/v1/reports?projectId=<id>` is the read-only persisted report
projection. Consumers such as Project Summary can read ordered report facts
and outputs through it without claiming, acknowledging, waking, or interrupting
delivery.

If `--agent` / `--harness` is omitted, `tallpine spawn` uses the resolved project's
`worker.agent` config. Before spawning, the CLI performs one targeted launch
ensure. It fails early for unsupported or definitely missing harnesses and
warns-but-continues for unauthorized or unknown observations; daemon session
creation repeats launch validation and native launch remains authoritative.
`--skip-agent-check` suppresses only the CLI warnings and early check, never the
daemon validation.

Standalone spawns require `--agent` because there is no project configuration
from which to resolve a default harness.

`tallpine spawn --output artifact` marks a worker whose deliverable is a file rather
than a pull request. The daemon persists the kind with the session and adds an
artifact-delivery section to the worker's system prompt, including after
restore and agent switching. It does not change derived status or the Kanban
column: the session shows artifact output once a file exists in its artifact
directory. Any other value exits 2; the API rejects unknown `requestedOutput` values
with `400 INVALID_REQUESTED_OUTPUT`.

`tallpine preview` resolves its session from the `TALLPINE_SESSION_ID` environment variable
(it is meant to run inside a session), not a flag. With no argument it
autodetects an `index.html` in the session workspace. Relative file targets are
resolved from the session workspace root, regardless of the shell's current
directory, and served through Tallpine's confined loopback preview origin. Absolute
paths and `file://` URLs must resolve inside that workspace; explicit HTTP and
HTTPS targets remain regular browser URLs.

`tallpine preview start [configuration]` loads `.tallpine/launch.json` from the session
workspace, starts that exact command under a session-owned supervisor, selects
or records its loopback port, waits for readiness, and opens application
targets in the Browser panel. `status` reports bounded recent logs and `stop`
terminates the managed process tree. Multiple configurations must be selected
by name; Tallpine does not assign confidence scores to arbitrary localhost servers.
This is an optional, reusable project configuration, not a prerequisite for
preview. Agents must not create it automatically. Static HTML and Markdown use
the direct file preview and must not cause package-manager scaffolding,
dependency installation, or a development server to be introduced.

When a browser-displayable file is the requested artifact, agents should call
`tallpine preview <workspace-path>` immediately after creating or materially updating
the primary output. Markdown, HTML, PDF, SVG, and common images can be served
directly. Supporting assets must not replace an active application preview.

`tallpine browser` also resolves its target from `TALLPINE_SESSION_ID`, but controls the
session-owned live Electron browser rather than only setting its preview URL.
The target-isolated command set includes `status`, `open`, `snapshot`, `click`,
`dblclick`, `focus`, `fill`, `type`, `press`, `hover`, `scroll`,
`scrollintoview`, `drag`, `select`, `check`, `uncheck`, `get`, `highlight`,
`unhighlight`, `tabs`, `tab new`, `tab select`, `tab close`, `frame`, `dialog`,
`wait`, `screenshot`, `network start/status/list/stop/clear`, `console`, and
`errors`. The native engine is bound internally; there is no second command or
connection setup. Logical tab IDs remain stable for the session, and allowed popups
become Tallpine browser tabs rather than separate OS-browser windows. The Tallpine desktop
app must be open because Electron owns the `WebContentsView`.
References from a snapshot are invalidated after navigation or DOM replacement;
they are also invalidated when changing tabs. Take another snapshot when a
command reports `STALE_REFERENCE`.
Browser waits cover load completion, text or selector appearance and
disappearance, URL matching, fixed delays, and a configurable DOM-stability
window for HMR-driven verification.
Temporary browser profiles are isolated per worker. Named profiles persist and
can be reused across sessions, intentionally sharing cookies and storage.
Profile selection does not broaden the session authorization on browser commands.
Network capture is disabled by default and must be started explicitly. It is
scoped to the active tab at start time, expires after 60 seconds by default
(maximum 300), retains at most 200 in-memory entries, and is cleared with the
tab/session. Captured data is metadata-only: request and response bodies are
never read, sensitive headers are omitted, and URL credentials, fragments, and
query values are redacted.

`go run .` in `backend/` remains a compatibility wrapper around the daemon.

PR actions are available through `tallpine pr merge` and
`tallpine pr resolve-comments`. `tallpine pr merge <number>` looks up the PR URL from tracked sessions and pins the
merge to the head SHA of the latest Tallpine-approved review of that PR, refusing when
there is none (pass `--expected-sha` for a head you reviewed yourself; `--url`
is required when the number is tracked in several repositories). Review actions are available through `tallpine review ls`,
`tallpine review trigger` (also `execute` and `restart`), `tallpine review cancel` (also
`stop`), and `tallpine review submit`.

## Configuration

The CLI and daemon share the same environment-driven config:

| Var                   | Default              | Purpose                                                                                        |
| --------------------- | -------------------- | ---------------------------------------------------------------------------------------------- |
| `TALLPINE_PORT`             | `3101`               | Daemon origin port: the app names daemon URLs with it; TCP listens on it only with `TALLPINE_LOOPBACK_TCP`. |
| `TALLPINE_LOOPBACK_TCP`     | unset (off)          | `1` also opens loopback TCP on `TALLPINE_PORT` beside the user-only socket, for clients that cannot dial a Unix socket (external browser, `npm run dev:web`). |
| `TALLPINE_RUN_FILE`         | `~/.tallpine/running.json` | PID/port handshake.                                                                            |
| `TALLPINE_DATA_DIR`         | `~/.tallpine/data`         | SQLite data directory.                                                                         |
| `TALLPINE_REQUEST_TIMEOUT`  | `60s`                | REST request timeout.                                                                          |
| `TALLPINE_SPAWN_TIMEOUT`    | `TALLPINE_REQUEST_TIMEOUT` | Base budget for spawn / orchestrator start / restore requests. Raise on a loaded machine if `tallpine spawn` returns `SPAWN_TIMEOUT`. It replaces (rather than extends) `TALLPINE_REQUEST_TIMEOUT` for those routes, so keep it at or above it. |
| `TALLPINE_TMUX_COMMAND_TIMEOUT` | `5s`             | Per-command timeout for tmux calls. An invalid value is rejected at daemon start, like the other timeouts. |
| `TALLPINE_SHUTDOWN_TIMEOUT` | `10s`                | Graceful shutdown cap. Also scales how long a new daemon waits for the data dir (9x + 18s).    |
| `TALLPINE_KEEP_DAEMON`      | unset (off)          | Keep the desktop app's daemon running after the window closes; stop only via `tallpine stop`. (fork) |
| `TALLPINE_DISABLE_GPU`      | unset (off)          | Skip Chromium hardware acceleration; escape hatch for broken Linux GPU drivers.                |
| `TALLPINE_DAEMON_AUTH`      | unset (enforced)     | `off` disables the loopback `X-VO-Token` check, only with `TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1` and only in an unstamped dev build (release builds ignore both). Debugging only. |
| `TALLPINE_SESSION_TOKEN`    | set by the daemon    | An agent session's scoped credential. For calls to the daemon that issued it, the CLI sends it instead of `daemon.token`, and the daemon limits the call to that session's capabilities. |
| `TALLPINE_SESSION_TOKEN_RUN_FILE` | set by the daemon | The issuing daemon's run file. The CLI sends `TALLPINE_SESSION_TOKEN` only when its own run file resolves to this one, and otherwise uses that daemon's `daemon.token`. |

The daemon serves the user-only socket `sock/daemon.sock` next to `running.json`
and, by default, no loopback TCP port. It requires the per-install token
in `daemon.token` (next to `running.json`, mode 0600) as `X-VO-Token` on every
request; the CLI reads it automatically. To call the API by hand:
`curl --unix-socket ~/.tallpine/sock/daemon.sock -H "X-VO-Token: $(cat ~/.tallpine/daemon.token)" http://127.0.0.1:3101/api/v1/sessions`.
See [ADR 0007](../adr/0007-authenticated-loopback-listener.md). Inside an agent session the CLI sends
the session's scoped `TALLPINE_SESSION_TOKEN` instead, and the daemon answers calls outside that session's
scope with 403 `AGENT_SCOPE_FORBIDDEN`; see [ADR 0008](../adr/0008-scoped-agent-session-tokens.md).
Connect Mobile is a separate,
opt-in authenticated listener; see [network boundaries](../architecture.md#multi-listener-architecture-loopback--lan).

## Manual smoke test

```bash
cd backend
go build -o /tmp/tallpine ./cmd/tallpine

tmp=$(mktemp -d)
export TALLPINE_RUN_FILE="$tmp/running.json"
export TALLPINE_DATA_DIR="$tmp/data"
export TALLPINE_PORT=3037

/tmp/tallpine status --json
/tmp/tallpine doctor
/tmp/tallpine daemon &
# In another shell with the same TALLPINE_* values, after /readyz succeeds:
/tmp/tallpine status --json
/tmp/tallpine stop
/tmp/tallpine status --json
rm -rf "$tmp"
```

## Adding new commands

Add a product command only when a daemon HTTP route owns the corresponding
mutation/read; the CLI must call that route rather than reimplementing daemon
behavior. Commands not yet exposed but with backend routes in place include
`tallpine events ...` (over the CDC/SSE endpoint) and CLI parity for PR/review
actions.

Do not port old in-process TypeScript CLI behavior that mixed command handling
with storage and runtime implementation details.

### Project origin and claim trust

A project's `repo` is the `origin` URL read once when the project is registered.
It is never re-read from the checkout afterwards: linked worktrees share
`.git/config`, so an agent could repoint `origin` and otherwise authorize claims
on a foreign repository. If the upstream repository is renamed or moved, set
`canonicalRepoURL` in the project config (a human-controlled daemon API write,
e.g. `tallpine project set-config`) so `tallpine session claim-pr` accepts the new URL.

### Claiming workspace PRs

Workspace projects can claim a PR/MR on their root origin or any registered
child repository origin. Use the child's full PR/MR URL: numbers still resolve
against the root's canonical repository or origin, and a root without a remote
cannot resolve numbers. Check registered children with `tallpine project get <id> --json`.
Unregistered repositories are rejected even if a checkout has an additional Git
remote for them. `canonicalRepoURL` requires a valid root origin; it is not a
workspace child allowlist. Scratch projects cannot claim PRs.

For automatic attribution, workspace sessions recorded on a bare branch such as
`tp/ws-1` or `tp/ws-1-2` can use hyphen siblings (`tp/ws-1-fix` or
`tp/ws-1-2-fix`) in registered repositories. Keep the entire recorded branch,
including collision suffixes. Exact and stacked branches and `/root` slash
siblings remain supported. Matching prefers the most specific owner and leaves
ambiguous ownership for explicit claiming. Custom branches and single-repository
projects do not gain hyphen-sibling ownership.

### Claiming upstream PRs from a fork

The registered origin remains the checkout and push repository. An optional
`canonicalRepoURL` in project config explicitly authorizes one upstream repository
for PR claims. Git remotes, including a remote named `upstream`, never grant claim
permission automatically. The canonical URL may be on a different host than the
origin: that is how a project follows a repository that moved (for example from
Bitbucket to GitHub). It then replaces the origin for PR tracking, native review
and GitHub account resolution, while checkout and push stay on origin. GitHub
credentials are only scoped to the canonical host when it is a GitHub host.
Claims match the entire namespace and repository, including GitLab subgroups.

For a project with no other config:

```bash
tallpine project set-config my-project \
  --canonical-repo-url https://github.com/my-org/my-repo
```

Focused flags such as `--canonical-repo-url` merge into the existing config
(`--config-json` and `--clear` replace it). The same full-object shape is accepted by `PUT /api/v1/projects/{id}/config` as `{"config": {...}}`.
Use an HTTPS repository URL, without a PR/MR suffix, credentials, query, or fragment.
Self-managed GitLab URLs and nested namespaces are supported. Explicit ports
are preserved and must match too; `gitlab.example.com:8443` is a different
authority from `gitlab.example.com`.

Both `tallpine session claim-pr 42` and `tallpine spawn --claim-pr 42` resolve numbers against
canonical when configured, otherwise origin. A full PR/MR URL may name either
identity. Unrelated repositories and different hosts/providers remain rejected.
Removing `canonicalRepoURL` restores origin-only claims. This does not unlink PRs
already claimed or move existing worktrees. Repository identity is read at claim
time, so existing sessions need no restart or duplicate project.

Migration 0126 adds an empty canonical identity to existing non-NULL config JSON
where absent, preserving all other settings and any explicit canonical value.
NULL configs retain their defaults. No Git discovery runs during migration, and
no earlier migration is modified. Downgrading preserves config data; older
versions do not support canonical claims and may drop this field when saving
project settings.
