# Enterprise agent policy

Status: permission cap, GitHub credential helper and environment allowlist shipped; the helper and the allowlist default to off.

Agents can run with full access and with the daemon's environment. A prompt
injection in a repository file, issue or web page can then ask the agent to run
commands with those privileges. The agent policy lets an administrator, or the
user, lower that. **It reduces the risk; it is not a sandbox.** Files such as
`~/.ssh/*` stay readable by any command an allowed agent runs, and network
egress is unrestricted. Closing those needs an egress and filesystem sandbox
(for example a per-session `sandbox-exec` profile), which is a separate
follow-up.

The default policy changes nothing: an install with no managed file, no
`TALLPINE_AGENT_*` variable and an untouched setting behaves exactly as before.

## Permission cap (`maxPermission`)

| Level | Effect |
|---|---|
| `none` (default) | No cap. |
| `auto` | Bypass / full access is forbidden. Claude Code and Codex `auto` stay allowed (unattended workers keep running). Kiro `auto` is a scoped permissions-rules profile (workspace read/write plus a allow-list of specific git/gh/report verbs and fixed build/test commands; interpreters and package runners still ask), so it stays `auto`; Kiro bypass (`--trust-all-tools`) becomes `auto`. Codex `default` and unknown modes, which mean full access today, become `auto`. |
| `accept_edits` | Strict. Bypass and `auto` become accept-edits (Codex: workspace-write with on-request approvals). Workers will pause for command approvals. |

Under either cap, Codex terminal sessions are also launched with `--sandbox workspace-write`, so a `sandbox_mode = "danger-full-access"` in the user's `~/.codex/config.toml` cannot undo it. Without a cap no flag is added. A capped Codex terminal or chat session also gets `sandbox_workspace_write.network_access=true` (Codex's workspace-write has no network by default, which would break `git push`, `gh` and package installs) and the same writable build-cache roots as the Claude fence; the cap limits files and approvals, not the network. In a linked worktree it also makes the worktree's git dir and the shared `objects/` and `refs/` writable so `git fetch`/`commit`/`push` work; the shared git `config`, `hooks/` and `packed-refs` stay read-only. Capped Codex also gets the test-slot pool writable. The Claude fence additionally allows the test-slot pool (`<data>/verify-slots`) so `scripts/go test` can queue.

Claude Code launched in `default` otherwise takes its starting mode from the user's `~/.claude/settings.json`, which may say `bypassPermissions`. Under a cap, Claude terminal and chat sessions pass `default` explicitly (`--permission-mode default`, or the ACP `default` mode).

DeepSeek chat sessions read their sandbox from `DSH_PERMISSION_MODE`; under a cap, any value inherited from the project or daemon environment other than the documented sandboxed ones (`read-only`, `workspace-write`) is removed from the launch, and dsh falls back to its own `workspace-write` default.

The cap lowers a mode; it never raises one. It applies wherever a session's mode
is resolved: spawn, relaunch, restore of a session remembered in bypass, Chat
and terminal sessions, the mode remembered per project, and the chat drivers.
Sessions already running keep their mode until they are relaunched.

## How the policy is built

The policy is a set of **rules** (`backend/internal/agentpolicy/rules/<rule>/`). Today there is one, `permissionCap`; the environment, GitHub credential and egress rules plug into the same shape. A rule is one package with a settings **schema** (fields, kinds, defaults, validation, i18n keys, whether a project may override a field), a pure `Decide` step, and effect methods on its outcome that enforcement seams call. Adding a rule is its package plus one line in `rules/all.go`: the engine, the API (`GET/PATCH /api/v1/settings/agent-policy`), `tallpine policy show` and the Settings section render and validate from the schema, so no per-rule API or UI plumbing is needed (a new kind of control, not a new rule, is the only UI change).

The session manager computes one immutable **decision** per spawn, relaunch and restore and hands it to every seam (agent launch, chat start and resume, interface transitions, account moves, reviewer chats). Seams read effects from the decision and never re-read settings. The one sanctioned live read is for per-turn input: a chat switching permission mode mid-session is held to the stricter of its frozen cap and the cap in force now (`LowerLive`), so a managed tightening applies to a chat decided before it.

Each launch's decision record (rules, settings, the layer that set each, and what a seam changed, such as `requested -> effective` permission mode) is stored per session and logged. It holds names and policy values only, never a secret. Read it with `tallpine policy show --session <id>` or `GET /api/v1/sessions/{id}/agent-policy`. The daemon also logs every non-default setting at startup.

## Where the policy comes from

Highest precedence first. A setting set by managed config or the environment is **locked**; Settings shows it read-only with its source, and the API refuses to change it (409 `POLICY_LOCKED`).

1. **Managed file**, written by an administrator or MDM. Read-only for the app.
   - macOS: `/Library/Managed Preferences/<bundle id>.plist` (a Configuration Profile payload; read with the system `plutil`). A plain JSON file at the same path also works.
   - Linux: `/etc/tallpine-daemon/policy.json`. Windows: `%ProgramData%\Tallpine\policy.json`.
   - Payload **version 2**: `{"schemaVersion": 2, "rules": {"permissionCap": {"max": "auto"}}}`. **Version 1**, the original flat file (`{"maxPermission": "auto"}`), stays accepted. Unknown keys in a v1 file are ignored silently; an unknown rule or setting in a v2 file, or a `schemaVersion` newer than the app knows, applies what it understands and shows a warning. The file is re-read at most every 30 seconds, so a profile push applies to new sessions without a restart.
   - A missing or unreadable file is no layer. A file that is present but malformed or invalid applies the strictest value of every setting that has one (the permission cap: `accept_edits`), locked, plus a warning in Settings and the daemon log.
2. **Environment**: `TALLPINE_AGENT_<RULE>_<SETTING>` derived from the schema (`TALLPINE_AGENT_PERMISSION_CAP_MAX=none|auto|accept_edits`), with the original `TALLPINE_AGENT_MAX_PERMISSION` kept as an alias. A value that is set but invalid (for example `accept-edits`, with a hyphen) applies the strictest value for a setting that has one and warns; otherwise it is ignored with a warning.
3. **Project override**: per project, `PATCH /api/v1/projects/{id}/agent-policy`. Security settings are **tighten-only**: a project may make agents stricter than the user's global value, never looser (a loosening write is refused, and a stored one is ignored with a warning). The override applies only to that project's sessions and never beats managed or environment values.
4. **User setting**: Settings, Harness, Agent policy (stored in the daemon). The stored document is version 2 (`{"version": 2, "rules": {...}, "projects": {...}}`); the PR 1 flat document is read and rewritten on the next save. A daemon older than the rule architecture ignores the v2 document, so downgrading drops the user layer (managed and environment layers keep working).
5. **Default**: no restriction.

A repository file is never a source: a `.tallpine/policy.json` in a repo would let a prompt-injected change weaken the policy.

`TALLPINE_MANAGED_POLICY_FILE` points the daemon at a different managed file, but only in development and test builds. Release builds ignore it, so a user cannot point the daemon at a weaker file.

## Enabling policies for your organization

Nothing here is enforced by default, for new installs or existing ones. A policy turns on only when an administrator sets it in the managed layer (below) or a user opts in in Settings, Harness, Agent policy. The managed layer beats everything a user or an agent can change.

Pick what you want, then deliver it:

| Goal | Managed setting |
|---|---|
| No bypass or full-access modes for agents | `permissionCap.max` = `auto` (or `accept_edits`) |
| Agents never receive a GitHub token in their environment | `githubCreds.mode` = `helper` (or `none` for no GitHub access) |
| Agents start with a curated environment, not the daemon's whole one | `envFilter.mode` = `allowlist`, plus `envFilter.passthrough` for your own variables |
| Spread new sessions across several signed-in accounts of a harness | `accountPool.placement` = `random`, `most-headroom` or `least-used` (default `default`: the default account) |
| Move a usage-limited session to another account with headroom (needs auto-resume armed on the turn) | `accountPool.failover` = `true` |
| Purge data of ended sessions after N days (see "Session retention") | `sessionRetention.enabled` = `true`, `sessionRetention.days` = `7` |
| Pin when projects plan, or which agent plans (see "Planner") | `planner.mode` = `auto`, `always` or `off`; `planner.harness` / `planner.model` |

**macOS (Jamf Pro, Kandji, Intune, any MDM).** Deliver a Configuration Profile whose custom-settings payload has the preference domain `ee.justdev.tallpine` and the keys `schemaVersion` (integer `2`) and `rules` (a dictionary of rule name to settings). [`docs/examples/agent-policy.mobileconfig`](examples/agent-policy.mobileconfig) is a complete sample that turns on all three; delete the rules you do not want and give the two payloads fresh UUIDs and your own identifier. In Jamf Pro: Computers, Configuration Profiles, New, Application & Custom Settings, External Applications (Upload), preference domain `ee.justdev.tallpine`, and paste the property list of the payload dictionary; or import the edited `.mobileconfig` directly. The MDM client writes the managed preferences file the daemon reads (see "Where the policy comes from"). Scope the profile to the computers, not to users, so it applies to every account on the machine.

**Linux and Windows.** Write the same payload as JSON to the managed path: `/etc/tallpine-daemon/policy.json`, or `%ProgramData%\Tallpine\policy.json`, readable by everyone and writable only by administrators:

```json
{"schemaVersion": 2, "rules": {"githubCreds": {"mode": "helper"}, "envFilter": {"mode": "allowlist"}, "permissionCap": {"max": "auto"}}}
```

**Check it worked.** On a managed machine: `tallpine policy show` lists each setting with its source (`managed, locked`); Settings, Harness, Agent policy shows the same controls read-only; the daemon log has one `agent policy: setting active` line per non-default setting at startup; and `tallpine policy show --session <id>` shows what a given launch got, including the names (never the values) of dropped environment variables. A profile push applies to new sessions within 30 seconds; running sessions keep the environment they started with, except that the GitHub credential endpoint decides again on every request. Removing the profile returns the machine to the user's own settings.

**Roll out gradually.** Start with `permissionCap` and `githubCreds=helper`, which rarely break anything. Turn on `envFilter=allowlist` for a pilot group, read `tallpine policy show --session <id>` after a failed tool to see what was dropped, and add the missing names to `passthrough`. `scripts/policy-exercise.sh` shows the whole flow on a scratch machine state: it starts a throwaway daemon and a real session without a policy and again with a managed file enabling `githubCreds=helper` and `envFilter=allowlist`, and checks that the agent sees no stray secrets, gets a token only through the helper and the `gh` shim, can still push, run `ssh`, `node` and `go`, fails closed when the daemon is down, and leaves a decision record without any token value. It never touches `~/.tallpine`, the running app or its tmux server, and needs a development build of `tallpine` (release builds ignore the managed-file override it uses).

This reduces what an agent can reach; it is not a sandbox. Files such as `~/.ssh` and network egress need the sandbox follow-up (see "Honest limits").

## Desktop app updates

The macOS app checks GitHub Releases on `justvibedev/tallpine` at launch and every 6 hours, downloads a newer signed build in the background, and shows "Restart to update" in the sidebar. It is on by default; users turn it off in Settings, General, Updates. Administrators decide it with top-level keys in the same `ee.justdev.tallpine` payload (next to `schemaVersion` and `rules`; the daemon ignores them):

| Key | Type | Effect |
|---|---|---|
| `AutoUpdate` | boolean | `false` turns updates off; `true` keeps them on whatever the user chose |
| `AutoUpdateChannel` | string | `stable` (full releases) or `beta` (also prereleases) |
| `AutoUpdatePinnedVersion` | string | Take only release `vX.Y.Z` from GitHub; never downgrades. A value that is not a version turns updates off |
| `FirstRunNoticeAccepted` | boolean | `true` pre-accepts the first-run notice (agents act with the user's permissions), so it is never shown |

Any of these keys makes the Settings controls read-only. A payload that cannot be read turns updates off. The updater talks only to github.com and sends no telemetry; Squirrel installs only builds signed by the same Developer ID team. Dev and locally built apps never update themselves.

## Settings managed by your organization

Besides `rules` (agent policy) and the update keys above, the same `ee.justdev.tallpine` payload can set and lock a small, fixed list of ordinary settings in a `settings` dictionary. A key present in the dictionary takes the given value on this Mac and is locked: the Settings field is read-only with a "Managed by your organization" note, and the API, the CLI and agents are refused with `SETTING_LOCKED` (HTTP 409) naming the key. Project keys apply to every project. A key you leave out stays under the user's control, and nothing changes without a managed file. Precedence is managed, then the user's (or project's) own value, then the default.

| Key in `settings` | Type | Controls |
|---|---|---|
| `app.cleanup.automatic` | boolean | Run storage cleanup automatically |
| `app.cleanup.worktrees`, `app.cleanup.artifacts`, `app.cleanup.sessionTemp`, `app.cleanup.harnessCaches`, `app.cleanup.systemTemp`, `app.cleanup.goBuildCache`, `app.cleanup.containers`, `app.cleanup.branches`, `app.cleanup.backlog` | dictionary | The matching cleanup rule (same shape as the cleanup policy in `GET /cleanup`) |
| `app.mobile.enabled` | boolean | Whether the mobile companion is available |
| `app.dynamicOrchestratorContext` | boolean | Whether orchestrators get live project context |
| `app.providerEventRetentionDays` | integer | Days provider events of a finished session are kept |
| `project.models` | dictionary | Models allowed in each project |
| `project.worker`, `project.orchestrator`, `project.planner` | dictionary | Harness, model and permissions of each role |
| `project.reviewers` | dictionary | The agents that review pull requests |
| `project.trackerIntake` | dictionary | Issue tracker intake |
| `project.autoReview`, `project.reviewRisk` | dictionary | Automatic pull request review and its risk rules |
| `project.terminateOnPrMerge` | boolean | End a session when its pull request merges |

`AutoUpdate`, `AutoUpdateChannel` and `AutoUpdatePinnedVersion` keep their top-level names (registry names `app.update.enabled`, `.channel`, `.pinnedVersion`). A value that does not validate keeps its key locked at the stored value, and a managed file that cannot be read or parsed locks every key in the table read-only (and turns updates off), so a broken profile never loosens a lock. The daemon re-reads the file about every 30 seconds.

```xml
<key>settings</key>
<dict>
	<key>app.cleanup.automatic</key>
	<true/>
	<key>app.providerEventRetentionDays</key>
	<integer>7</integer>
	<key>project.models</key>
	<dict>
		<key>denylist</key>
		<array><string>gpt-5-pro</string></array>
	</dict>
</dict>
```

Put it in the payload dictionary next to `schemaVersion` and `rules`. The key list comes from the settings registry (`MDMLockable` in `backend/internal/settingsreg`); `frontend/src/shared/managed-keys.json` and the Jamf Custom Schema (`frontend/src/shared/jamf-schema.json`, attached to every release as `tallpine-jamf-schema.json`) are generated from it, and a test fails when this table misses a key. See [Jamf Pro deployment](jamf.md).

## Honest limits

- The policy is a hard guarantee only when it comes from the managed file. A
  user who controls the daemon's environment or its database can change the
  environment and user layers.
- `acceptEdits` still lets allowed commands run; Codex `workspace-write` has no
  network but Claude Code does not. Egress control needs the sandbox follow-up.

## Session retention (`sessionRetention.*`)

Ended sessions keep their raw provider events, worktrees and transcripts until something removes them; on a busy machine that is gigabytes. The retention policy purges them once a terminated session is older than an age threshold. It is **on by default** (7 days): a user opts out (Settings, Harness, Agent policy, or `tallpine retention set enabled=false`), or an administrator sets it in the managed layer, which locks it. An install that never set it gets a one-time notice in the app.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `sessionRetention.enabled` | bool | `true` | Turn the policy on. |
| `sessionRetention.days` | int, 1-3650 | `7` | Age, since the session ended, after which its data is purged. |
| `sessionRetention.dropProviderEvents` | bool | `true` | Deprecated and ignored. Raw provider events follow `app.providerEventRetentionDays` (Settings > Storage). |
| `sessionRetention.dropWorktrees` | bool | `true` | Remove the worktree. A dirty one is salvaged first (patch, untracked files, unique commits) under `~/.tallpine/salvage`, as the worktree cleanup does. |
| `sessionRetention.dropTranscripts` | bool | `false` | Delete the transcript: messages, non-system activities, terminal turns. |
| `sessionRetention.includeOrchestrators` | bool | `false` | Also purge ended orchestrator sessions. Off by default: a fresh orchestrator chat reads its predecessors' history, so their messages, transcripts, turns and activities are kept. |

Always kept, whatever the settings: the session row (id, project, model, branch, created time), its conversations, turns, usage bindings and model usage events, `system` activities (compaction markers), reviews, PRs and app settings, so usage, cost, review and A/B analysis keep working. Live sessions are never touched: each purge re-reads the session first. The orchestrator's shared worktree (`worktrees/<project>/orchestrator/`) is never removed, even with `includeOrchestrators`, and neither is any worktree another session (live or ended) still points at. A session whose events or transcript were purged can no longer be restored (the restore is refused rather than duplicating replayed history).

It runs in the daily full cleanup pass. Every delete is a small batch under the database writer lock, and afterwards an incremental vacuum returns the freed pages to the OS in short steps, so the daemon is never blocked for long. Preview first: `tallpine retention preview` lists what would go and the space freed, with the current settings even while the policy is off; `tallpine cleanup --dry-run` includes retention only for projects where it is on. `tallpine retention run` applies it now. A project can override any field (`tallpine retention set --project <id> days=90`) unless the managed layer sets it. Managed file: `{"schemaVersion": 2, "rules": {"sessionRetention": {"enabled": true, "days": 7}}}`.

## Planner (`planner.*`)

The planner (B205) is a read-only agent that explores the repository, asks the owner at most five questions with recommended answers, and submits a plan the owner approves before an implementer builds it. It is **on by default**; `TALLPINE_PLANNER=off` on the daemon is a kill switch for development, not a product setting.

Each project chooses in its own config: `planningMode` is `auto` (default; the orchestrator decides, and the owner can say "plan this" or "just do it"), `always` (starting work on an item without an approved plan is refused: `tallpine plan start <ref>` or `--force`), or `off`. The planner's agent is the project's `planner` override (`harness`, `agentConfig.model`), else the project's orchestrator, else its worker. It never defaults to a vendor or to the strongest model.

The managed layer can pin these for every project. All three fields are empty by default and an empty field locks nothing, so nothing here turns planning off unless an administrator sets `mode` to `off`.

| Setting | Type | Default | Effect |
|---|---|---|---|
| `planner.mode` | `""`, `off`, `auto`, `always` | `""` | Overrides every project's `planningMode`. |
| `planner.harness` | string | `""` | Pins the planner's agent harness (for example `claude-code`); set it together with `model`, since a locked model alone drops the inherited harness. |
| `planner.model` | string | `""` | Pins the planner's model. |
| `planner.autoApprove` | `""`, `off` | `""` | `off` locks the per-project `planAutoApprove` setting off in every project (see below). An invalid managed value also locks it off. Managed file or environment only; the user layer ignores it (B525). |
| `planner.inPlace` | `""`, `off` | `""` | `off` locks the per-project `planInPlace` setting off in every project. An invalid managed value also locks it off. |

`planAutoApprove` (project config, default off) approves a submitted plan at once, with the same side effects as an owner approval, and records the approver as `auto (project setting)`. It exists for unattended throwaway test and benchmark projects. Only a person can turn it on (Settings → Project, or `tallpine project set-config <id> --plan-auto-approve`): every agent write path refuses it, including the project's own orchestrator, and `planner.autoApprove` = `off` locks it off.

Managed file: `{"schemaVersion": 2, "rules": {"planner": {"mode": "auto", "harness": "<harness>", "model": "<model id>"}}}`. A project's settings are not generically MDM-lockable yet (only policy-scope settings are), which is why the lock is an agent-policy rule that wins over `project.planningMode` and `project.planner`.

## Agent environment (`envFilter.mode`, `envFilter.passthrough`)

`inherit` (default) starts agents with the daemon's whole environment, as before. `allowlist` starts them with only:

- a curated base list: `PATH`, `HOME`, `USER`, `SHELL`, `LANG`/`LC_*`, `TMPDIR`, `XDG_*`, `SSH_AUTH_SOCK`, proxy variables (`HTTP(S)_PROXY`, `NO_PROXY`, ...), CA bundle variables, the git variables a shell needs (`GIT_SSH_COMMAND`, `GIT_AUTHOR_*`, ...), common toolchain locations (`GOPATH`, `NVM_DIR`, `CARGO_HOME`, `JAVA_HOME`, ...) and the Windows system variables;
- the variables the supported agent CLIs read for their own login: `ANTHROPIC_*`, `CLAUDE_CONFIG_DIR`, `OPENAI_*`, `CODEX_*`, Bedrock/Vertex routing (`AWS_*` credentials, `GOOGLE_APPLICATION_CREDENTIALS`), `KIRO_*`, `DSH_*`, `GEMINI_*`;
- your `passthrough` list: variable names, or a prefix with a trailing `*` (at most 100 entries, a variable name per entry);
- everything the daemon itself sets for the session: `TALLPINE_*`, the project's own environment, the agent account variables, the session scratch directory.

It is one list for every harness in this version. It narrows what an agent sees from "everything the daemon has" to "what the harnesses need"; the harness credentials in it are still readable by a prompt-injected agent. Per-harness lists are a follow-up. On macOS and Linux a listed name that is not a plain identifier (for example `ProgramFiles(x86)`) is dropped and reported as dropped, because the terminal launch script cannot export it. A daemon session token is never passed, even if listed. GitHub tokens are not on any list: they stay out when `githubCreds` is `helper` or `none`, even if you list them. `NODE_OPTIONS`, database URLs, other cloud keys and any other variable the daemon holds are dropped unless you list them.

How it is enforced: the session's environment map becomes the whole environment (a reserved launch key, `TALLPINE_SCRUB_ENV`, that each launch path honours and never passes on). Terminal agents and the native reviewer launch through `env -i`, so the result is exact even when a tmux server was started earlier with a different environment (new panes inherit the server's environment, not the daemon's); `TERM` comes from the pane. The ConPTY/pty-host runtime starts its host from the session's variables only, and Chat provider processes get the same. The allowlist does not apply to the daemon's own helper commands (git probes, hooks): they are not agents.

Diagnosing breakage: each launch's decision record lists the dropped variable **names** (never values) with counts, so "why did my agent lose X" is answerable: `tallpine policy show --session <id>`. If a tool fails because it needs a variable, add the name to `passthrough` (Settings, or `TALLPINE_AGENT_ENV_FILTER_PASSTHROUGH=A,B_*`). The mode is `TALLPINE_AGENT_ENV_FILTER_MODE=inherit|allowlist`; the earlier names `TALLPINE_AGENT_ENV_MODE` and `TALLPINE_AGENT_ENV_PASSTHROUGH` and the managed v1 keys `envMode` / `envPassthrough` still work. Project overrides of the mode are tighten-only; a project cannot add passthrough names. An invalid mode value is reported and ignored (failing closed over a typo would cut agents off from their own tools).

## GitHub credentials (`githubCreds.mode`)

Why agents hold a GitHub token at all: so `gh` and anything honouring `GH_TOKEN` act as the right account for the session's project (#198 picks the project's `gh` account) without touching the user's global `gh` login, which is shared state that parallel sessions would race on. An environment variable is the cheapest way to do that, and the worst place for a token: it shows in `env` dumps, logs, crash reports and the environment of every child process of every tool the agent runs. IDEs and Git clients use a credential helper instead: git asks for a credential when it needs one and nothing keeps it.

| Mode | Behaviour |
|---|---|
| `env` (default) | Today's behaviour: the project's account token is placed in the session's environment as `GH_TOKEN`. |
| `helper` | No token in the environment. git asks `tallpine git-credential`, and `gh` runs through a shim, each asking the daemon for the project's token **per use**. |
| `none` | No GitHub access. Every request is refused; other credential helpers are still reset for the GitHub hosts so the user's keychain token is not offered. |

How `helper` works, for a session whose project remote is on `github.com` (or a GitHub Enterprise host, which is added to the scope):

- gh login file: Codex, Kiro and other harnesses that run commands through the user's shell have their rc files put the real `gh` ahead of the shim, so for those harnesses (not Claude Code, whose shim always wins) helper mode also writes the project account's token to `hosts.yml` in the session's private gh config directory (the session temp dir; directory 0700, file 0600; never in the environment, the worktree or home). It is removed and rewritten at every launch of the session, so a relaunch under mode `none` leaves no login, and it is deleted with the session. A `gh` that reads it skips the per-request decision and audit line, and a token rotated or a policy pushed to `none` while the session runs only applies at its next relaunch. A token the project's own environment sets wins and no file is written.
- git: the session's environment carries a `GIT_CONFIG_PARAMETERS` entry (not `GIT_CONFIG_KEY_n`: Codex and DeepSeek Harness drop environment names containing KEY, TOKEN, SECRET or PASSWORD from the commands they run) that, for each GitHub host, reset `credential.https://<host>.helper` (so the user's keychain helper is not offered for those hosts) and add `tallpine git-credential`. Other hosts keep their own helpers. A project's own `GIT_CONFIG_*` entries are kept; ours are appended after them.
- gh: a `gh` shim directory is first on the session's `PATH`. It runs `tallpine gh-exec`, which asks the daemon for the token, finds the real `gh` (skipping the shim) and runs it with `GH_TOKEN` set **in that child process only**. The session also gets a private per-session `GH_CONFIG_DIR` without the user's stored login, so a `gh` reached by absolute path (bypassing the shim) cannot use it (for Codex and Kiro that directory holds the login file described above, otherwise it is simply not logged in).
- The daemon's own `GH_TOKEN`, `GITHUB_TOKEN`, `TALLPINE_GITHUB_TOKEN` (and the enterprise variants) are removed from the agent's inherited environment (tmux, the pty host and Chat provider processes).
- The daemon endpoint (`POST /api/v1/sessions/{id}/github-credential`) is authenticated by the session's scoped token, for its own session only (a reviewer's own session is the worker under review). It **decides the policy again on every request**, so a managed push that sets `none` stops a running session's next request (not a `gh` that uses the login file above until the session relaunches). It resolves the project's account exactly as before (project `githubUser`, then `TALLPINE_GITHUB_USER`, then auto-pick), refuses hosts that are not `github.com` or the project's remote host, and logs one audit line per request naming the session, project, host and source, never the token.
- A token the **project's own environment** sets (`GH_TOKEN` and friends) is an explicit user choice: it keeps working and wins, in the environment as before and as the helper's answer. The decision record names the variable (never the value).
- The order matches `env` mode: a token the project's environment sets, then one the **daemon's** environment pins (`GH_TOKEN`, `GITHUB_TOKEN`, `TALLPINE_GITHUB_TOKEN`), then the project's `gh` account. A narrow token pinned on the daemon therefore keeps scoping agents when you turn on `helper`; it is served per use instead of being inherited.

What this does **not** do. An agent can still ask the helper itself: it can run `git credential fill` or `tallpine gh-exec` and read the answer. The gain is real but bounded: the token is no longer in env dumps, logs and child processes; every use is a per-call, per-session audited request; and the daemon can deny. It is not containment; that needs the sandbox follow-up. A prompt-injected agent that decides to push to its own repository still can, through the helper.

Failure modes. With the daemon down, git and gh fail with the helper's message (nothing is cached). If the daemon's executable cannot be resolved the session gets no helper and no token, and the daemon logs why. On Windows the helper and shim are not implemented yet, so `helper` falls back to the token in the environment and logs a warning; `none` is not affected (it never gives a token, and the daemon's tokens are still withheld). Settings: Settings, Harness, Agent policy, "GitHub credentials"; managed file `{"schemaVersion": 2, "rules": {"githubCreds": {"mode": "helper"}}}`; environment `TALLPINE_AGENT_GITHUB_CREDS_MODE`. Project overrides are tighten-only (`env` < `helper` < `none`). An invalid environment value is reported and ignored (failing closed to `none` over a typo would cut every agent off from GitHub).

### What `inherit` means for secrets, and the install token

Under the default `inherit`, every agent and every tool it runs (a prompt-injected `npm install` script, for example) sees whatever secrets are in the daemon's environment: cloud keys, `GH_TOKEN`, and so on. This is the default because policies are off unless you or your administrator turn them on. If the daemon's environment holds secrets you would not hand to an agent, start the daemon without them, or set `envFilter.mode` to `allowlist` (see above) and list only what agents need in `passthrough`.

The daemon's install token (the `daemon.token` file next to the run file) is never put in an agent's environment. Agents get a scoped session token instead (`TALLPINE_SESSION_TOKEN`, repeated as `TALLPINE_SESSION_AUTH` for harnesses that drop credential-named variables, bound to the session and its capability policy, see ADR 0008). The scoped token is a policy control, not a sandbox: the install token file is readable by the same OS user, so an agent that runs shell commands can read it unless its harness sandbox denies that. For Claude Code sessions we add a `permissions.deny` rule for the token file to the launch settings, which stops Claude's own Read tool from opening it; shell commands such as `cat` are only stopped by a real sandbox (for Codex, a policy cap that pins `workspace-write`, and an OS-level sandbox for the rest). Treat full containment as a follow-up.

## Scoping the GitHub token (env mode)

Without configuration, sessions of a project run as the user's `gh` account
(`GH_TOKEN` is set from it). To use a narrower credential instead, create a
fine-grained personal access token limited to the one repository (Contents,
Pull requests and Issues read/write) and add it to that project's environment as
`GH_TOKEN`. A token set in the project environment takes precedence over the
automatic account token.

Project environment variables are stored as plain JSON in the daemon's SQLite
database under `~/.tallpine`, protected by file permissions only, not encrypted. Do not
put a broad token there. A keychain- or reference-based secret for project
environment is a follow-up.

## Security mode (`securityMode.enabled`)

One switch for the install's baseline, **off by default** (fresh installs and upgrades alike; nothing changes until someone turns it on). When on, three rules are raised to a floor (shown with source `security_mode`): `protectState` on, `envFilter` `allowlist` and `githubCreds` `helper`. A looser value a user or project set for one of those rules is raised to the floor; a stricter one is kept. It changes no grant, permission mode or permission cap, and it never refuses a launch: only the hardened profile fails closed.

- **Settings > Security** offers one choice, Standard, Protected or Unrestricted. Standard resets `securityMode.enabled` and `devMode.enabled`; Protected sets `securityMode.enabled` and resets `devMode.enabled`; Unrestricted sets `devMode.enabled` and resets `securityMode.enabled`, all in one PATCH. The daemon refuses a user-layer write that would leave both on (`agentpolicy.ErrModeConflict`, also when managed config has Security mode on); a project-layer `securityMode.enabled` write is always accepted and takes effect once Unrestricted is left. A stored both-on combination is not touched by unrelated writes and resolves as before (Dev mode wins unless hardened); the tab shows a pick-one notice. The individual protections (`protectState`, `envFilter`, `githubCreds`, `permissionCap`) have no rows; the tab lists any set to a non-default value by file, CLI or managed config. The same tab holds the orchestrator switches (disabled under Unrestricted) and `repoAgentConfig`. `tallpine policy set security-mode on|off` does the same (turning it off needs the install token, not an agent token).
- **Project settings > General > Security mode** (disabled with a note while the app is Unrestricted; the daemon still stores the write) is inherit, on or off for one project. A project's off beats the install's on; a project may turn it on when the install has it off.
- **Locks.** A managed `securityMode.enabled=false` locks it off, `TALLPINE_AGENT_SECURITY_MODE` locks it from the environment, and the hardened profile implies on. Locked values show read-only in Settings. A managed per-rule value looser than a floor wins over the floor; only the hardened profile overrides managed values.
- **Upgrades.** Off by default, so an upgrade changes nothing and shows no notice. A stored `true` or `false`, at the user or project layer, is kept. Changes apply to sessions started, relaunched or restored from then on; running sessions are not restarted.
- **Not fenced.** Codex and DeepSeek Harness in their own sandboxed modes (Codex accept edits and auto, dsh outside bypass), every harness on Linux or Windows, and unknown harnesses cannot be fenced (macOS refuses a sandbox inside a sandbox). Claude Code and Kiro on macOS are fenced in every mode; full-access Codex and bypass dsh are fenced by a Tallpine seatbelt around the whole process. They launch with the other two floors, and the session's inspector shows "Not fenced: <reason>" with a link to the project switch.
- **Docker.** The fence closes the Docker socket unless the project sets `allowDocker` (owner-only; Docker gives an agent full access to the host, so Settings warns).
- Project environment encryption at rest still follows the hardened profile only; it moves onto Security mode in a follow-up change.

## Tallpine state fence (`protectState.mode`)

With `protectState.mode=on`, Claude Code sessions on macOS run their shell inside Claude Code's sandbox with reads of Tallpine state denied (the install token, session key, database, GitHub PAT, browser and Electron profile, other sessions' chat hosts), plus `permissions.deny` Read rules for Claude's own tools. It applies to terminal and chat sessions alike: both get the same settings from one builder, the terminal through `--settings` and chat through the ACP session's `claudeCode.options.settings`, which claude-agent-acp loads into the same settings layer. Commands that the sandbox denies cannot be retried outside it, and a host without a working sandbox refuses to run commands.

It is a file fence, not DLP: network egress stays unrestricted, and the shell may look up exactly two Mach services (`com.apple.trustd.agent` for `gh` TLS, `com.apple.CoreDevice.CoreDeviceService` for `xcrun devicectl`), and processes may bind loopback ports (`allowLocalBinding`) so dev servers, `httptest` and `vitest` run. The Docker socket is not allowed. Besides the worktree the shell can write the session temp dir, artifacts and the usual build caches (`~/Library/Caches`, `~/Library/Developer/Xcode/DerivedData`, `~/.cache`, `~/.npm`, and the gradle, maven, bun, rustup and pnpm cache subdirectories (never the tool homes, so nothing can persist into PATH binaries or init scripts), `go/pkg/mod`, `~/.cargo/registry`). `scripts/security-smoke` runs a work-loop matrix (go test, vitest, git fetch/push, gh, npm install, xcodebuild, port bind, tallpine CLI) so friction can be measured fenced vs unfenced. Kiro, full-access Codex and bypass DeepSeek Harness on macOS are fenced by a Tallpine-generated `sandbox-exec` profile around the whole process instead (`backend/internal/statefence/seatbelt.go`), with each harness's own state left open; see the public Agent policy docs page. Other harnesses, sandboxed Codex/dsh modes and every agent on Linux or Windows launch unchanged.

**Local dev tooling (`protectState.devTooling`).** Go test servers, dev servers and the merge gate (`scripts/merge-gate`, `tallpine merge`) bind Unix sockets, and the sandbox denies that anywhere it is not told. `allow` (the default) makes exactly two openings: the session's own temp dir (`$TMPDIR`, and `CLAUDE_CODE_TMPDIR` when set, whose `claude-<uid>` child is the shell's temp dir) is added to the sandbox's allowed Unix sockets, and the `xcrun_db-*` cache files in the user's per-user temp dir (`/var/folders/.../T`, and its `/private/var` form) become writable, because the Xcode shims behind `git`, `clang` and cgo write them regardless of `$TMPDIR`. Nothing broader is opened: not the data dir, the shared `/tmp/claude-<uid>`, the whole per-user temp dir or the home, and the secret paths stay denied. `strict` keeps the daemon socket directory as the only socket the shell may bind, so tests that bind a socket then fail. A project may set `strict` but never loosen a user or managed `strict`; managed policy and `TALLPINE_AGENT_PROTECT_STATE_DEV_TOOLING` lock it like any other field. It only matters while `protectState` is on. The default is `allow` because the opened directory is private to the session and already writable, while `strict` makes `go test ./...` and the merge gate fail inside the fence; the hardened profile does not change it, so set `strict` in the managed file where that is wanted. The merge gate itself needs no sandbox change for its log directory (it falls back to `$TMPDIR/merge-gate-logs/`), the xcrun cache (it sets `DEVELOPER_DIR`) or `node_modules` (it installs what `scripts/verify` needs).

## Hardened profile (`profile`)

One managed top-level key turns on a locked, strict configuration:

```json
{"schemaVersion": 2, "profile": "hardened"}
```

[`docs/examples/hardened-profile.mobileconfig`](examples/hardened-profile.mobileconfig) is a complete macOS sample. Only the managed file sets the profile (and `TALLPINE_MANAGED_POLICY_FILE` in development builds); no user setting or environment variable can. It locks these floors, shown with source `managed_profile` in `tallpine policy show` and as locked in Settings:

| Setting | Floor |
|---|---|
| `permissionCap.max` | `auto` (no bypass or full access, Codex never runs `never`/`danger-full-access`) |
| `envFilter.mode` | `allowlist` (an unknown harness gets no credential variables, not every harness's) |
| `githubCreds.mode` | `helper` |
| `protectState.mode` | `on`, and required |
| `repoAgentConfig.enabled` | `false` |
| `orchestratorApprovals.enabled` | `false` |
| `crossProjectOrchestration.enabled` | `false` |

Passthrough, account pool, planner and retention are untouched. An explicit managed value under `rules` is honoured only when it is at least as strict as the floor (`permissionCap.max=accept_edits`, `githubCreds.mode=none`); a looser one is ignored with a warning. A malformed or unknown profile value (`"strict"`, `1`) applies the hardened profile with a warning.

**Fail-closed launches.** Under the profile every agent launch must run inside the state fence: worker, orchestrator, planner and reviewer sessions, on spawn, relaunch, restore, account move and terminal/chat switch. Claude Code and Kiro on macOS can be fenced. Codex and DeepSeek Harness are fenced only with their own sandbox off, which the profile's `auto` cap forbids, so they are refused, as is an unknown harness and every agent on Linux or Windows, with error `POLICY_REQUIRES_FENCE` (HTTP 409) naming the harness and OS. Which harness a project may select in Settings is unchanged; the refusal happens at launch.

**Developer credential homes hidden.** Under the profile the fence also denies agent reads (sandbox `denyRead` and Claude `Read` deny rules) of `~/.ssh`, `~/.aws`, `~/.kube`, `~/.config/gcloud`, `~/.azure`, `~/.docker/config.json`, `~/.netrc`, `~/.npmrc`, `~/.pypirc`, `~/.config/gh`, `~/.git-credentials` and `~/.config/git/credentials`. Outside the profile (plain `protectState.mode=on`) they stay readable. Workers keep pushing: `githubCreds.mode=helper` supplies git and gh credentials through Tallpine's own path, and `SSH_AUTH_SOCK` stays reachable (the socket is added to the sandbox's allowed Unix sockets), so use an SSH agent rather than key files. `~/.ssh/known_hosts` and `~/.ssh/config` stay readable so ssh can verify hosts. Two optional lists under the `protectState` rule adjust it (a project may add `credentialPaths`; `credentialAllow` is honoured only from the managed file or the environment, and ignored with a warning from user or project settings): `credentialPaths` hides more paths, `credentialAllow` exempts paths from hiding. Entries are absolute or `~/`-relative.

```json
{"schemaVersion": 2, "profile": "hardened", "rules": {"protectState": {"credentialPaths": ["~/.config/op"], "credentialAllow": ["~/.npmrc"]}}}
```

**Running sessions** keep the decision they launched with and are not stopped. The profile applies at their next relaunch or restore; Settings and `tallpine policy show` say so.

**Limits.** It is a file fence, not DLP: network egress is unrestricted, so anything an agent may read can leave the machine. It does not yet keep one session from reading another session's worktree.

**Project environment encrypted at rest.** Under the profile, each project environment value (Settings > project > Environment) is stored AES-256-GCM encrypted in Tallpine's database, bound to its project and variable name. The key is a random 256-bit value in a generic-password item `ee.justdev.tallpine.project-env` in your login Keychain, created by the Tallpine daemon itself through the Security framework, so the item trusts only the daemon's own code; Tallpine never creates or reads it with `/usr/bin/security`. It looks only in the login keychain file, and refuses an existing item whose access list lets any other application read it (for example one planted with `security add-generic-password`): delete that item and restart Tallpine. Tallpine also records a fingerprint of the key it created in its own data dir (`project-env-key.fingerprint`, behind the state fence) and accepts only that key. On first use it deletes any pre-existing item and creates a fresh one, so a key planted ahead of time (even one trusting only the Tallpine daemon) is never adopted. If the item is later lost (deleted, Keychain reset, new Mac) and no encrypted value is stored, Tallpine creates and records a new key; while encrypted values remain, it refuses a different key because it could not open them. On the first start under the profile, existing plaintext values (archived projects included) are encrypted in place in the background, one project at a time.

If the key cannot be read (Keychain locked, access denied, or after an update macOS asks again and you decline), that project fails closed: its settings cannot be opened or saved and every launch is refused with `PROJECT_ENV_LOCKED` (HTTP 409); its values are never rewritten as plaintext and sessions never start without them. The project still appears in the list. Unlock the Keychain or allow access and retry; after a failed read Tallpine waits a minute before asking the Keychain again, so a declined prompt is not repeated for every project. Removing the profile stops encrypting new writes; values already encrypted stay encrypted and readable while the key is available. If the key is gone for good (Keychain item deleted or reset), the encrypted values cannot be recovered: remove the project (this drops its encrypted values, keeping any plaintext ones), add it again and re-enter its environment variables. Projects removed earlier do not get in the way: their encrypted values that can no longer be opened are dropped when they are re-added. Removing a project while the Keychain is only temporarily locked drops its encrypted values too, so unlock first if you want to keep them. On Linux and Windows there is no key store, which does not matter since the profile refuses every launch there.

## Account pool

The `accountPool` rule is not a restriction: it switches on spreading sessions across several signed-in accounts of one harness (Claude Code, Codex). Both settings are off by default. `placement` picks the account a new session without a chosen one starts on; `failover` moves a session whose account hit a usage limit to another account with headroom, instead of waiting for the reset. Settings shows them under Accounts, Account pool. They follow the layers above, and `TALLPINE_ACCOUNT_PLACEMENT` and `TALLPINE_ACCOUNT_FAILOVER` (`on`/`off`) are documented environment overrides that lock the setting like any other environment value. Rotating several personal subscriptions to avoid usage limits may conflict with the provider's terms; for teams, use organization seats or API keys.
