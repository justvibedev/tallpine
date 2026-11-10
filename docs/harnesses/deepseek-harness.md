# DeepSeek Harness

Tallpine runs [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)
(`dsh`) as the fourth harness beside Claude Code, Codex and Kiro. The harness id
is `deepseek-harness`; the binary it resolves is `dsh`.

```bash
npm install -g @deepseek-ai/dsh     # or Settings → Agents → Install
tallpine spawn --harness deepseek-harness
```

## Chat (primary path)

Chat launches `dsh --profile acp` through the shared ACP chat driver
(`chatdriver/deepseekharnessacp`), so it gets the same turn lifecycle as the other
harnesses: the auto `turn_ended`/`exited` worker reports, queued sends,
host-lost auto-resume, and `AGENT_STOPPED` on a send to a stopped agent.

- **Models.** The picker lists the `model` select that a short-lived ACP session
  advertises. Values are opaque JSON-array strings such as
  `["deepseek-official","deepseek-v4-flash"]` and are sent back unchanged.
- **Effort.** dsh offers `off`, `low`, `high`, `max`. Tallpine's vocabulary maps
  `none`/`minimal`/`off`→`off`, `low`→`low`, `medium`/`high`→`high`,
  `xhigh`/`max`→`max`. An unknown value is passed through and rejected by dsh.
- **Resume.** ACP `session/resume`; dsh does not replay history on resume.
- **Role prompt.** Sent as a text block ahead of the first turn of a new
  conversation (dsh has no system-prompt flag), not repeated on resume.
- **Permissions.** `auto` answers every `session/request_permission`;
  `accept-edits` answers edit/delete/move tools; other modes leave the decision
  to dsh.

## Terminal

Terminal runs the one-shot `dsh --profile headless "<task>"` and exits; it is not
an interactive TUI and is greyed out as a switch target (Chat is the real path).
The role prompt leads the task inside an `<ao_standing_instructions>` block. There
is no model flag; the profile's own route is used. A terminal session has no
native session id, so restore falls back to a fresh run. On Windows the role
prompt rides argv and can hit the `.cmd` shim's ~8k command-line cap; use Chat.

## Privacy defaults

dsh mounts `@deepseek-ai/dsh-session-log-deepseek` in every shipped profile,
**enabled by default**: it uploads the canonical session log with each
official-API request. Every Tallpine launch (Chat, model discovery, Terminal) therefore
adds a per-launch overlay and never edits your profile:

```
dsh --profile acp --patch $TALLPINE_DATA_DIR/dsh/privacy.patch.yml
# - id: session-log-deepseek
#   config: {enabled: false}
```

Tallpine also sets `DSH_TELEMETRY_MODE=DISABLED` (dsh's OTLP session export defaults to
`FEEDBACK_ONLY`) unless the project environment sets its own mode. To opt back in,
set `DSH_TELEMETRY_MODE` in the project env, or enable the plugin in your own
profile; the overlay is applied last, so remove it by launching dsh yourself.

**Opening a Tallpine session in plain dsh.** The upload plugin resumes its
unaccepted suffix when re-enabled. If you later open a Tallpine-created dsh session in
dsh itself with upload enabled, dsh uploads that session's whole backlog. Tallpine keeps
the default `DSH_HOME` (moving it would break dsh's own auth and sessions), so
disable the plugin in your own profile if that matters to you.

The role prompt is a stable prefix (no timestamps, ids or branch names ahead of
the task) so DeepSeek's prefix cache keys on it, and Tallpine never triggers
compaction; dsh's own policy owns that.

## Credentials

dsh owns its credentials (`~/.dsh/.credentials.yaml`, or `$DSH_HOME`). Save a
DeepSeek key once through dsh's web Models page (**Set up DeepSeek** runs
`dsh --profile web` for you); Tallpine uses that saved key for both Chat and Terminal,
and no environment variable is needed. Tallpine only reports whether a
`DEEPSEEK_API_KEY` ref (or env var) exists, never the value.

dsh's `headless` profile composes its credentials plugin *disabled*, so a plain
`dsh --profile headless` fails with `MISSING_CREDENTIAL` even with a saved key.
Tallpine's Terminal launch therefore adds a second per-launch overlay
(`$TALLPINE_DATA_DIR/dsh/credentials.patch.yml`: `- id: credentials` / `disabled: false`).
The `acp` and `web` profiles already enable it, so Chat adds nothing.

## Limits

- Multi-repository workspace projects are not supported over ACP (dsh rejects
  additional directories).
- Reviewer: read-only (`adapters/reviewer/deepseek`).
- Terminal hooks: the headless launch inserts dsh's bundled `@deepseek-ai/dsh-hooks-claude-code` plugin through a `hooks.patch.yml` overlay and a generated `hooks.json` under `<data dir>/dsh`, so SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop and SubagentStart/Stop run `tallpine hooks deepseek-harness <event>`. That gives terminal activity/idle, turn end and subagent tracking. Pinned to dsh `0.2.1-rc.2` (documentation only; nothing enforces it). Known gaps: `transcript_path` is empty, there is no permission-request event so terminal approvals stay degraded (ACP chat approvals work), and a child agent's own hooks are dropped by session id, assuming the root id never rotates within a launch. Chat (ACP) does not mount the hooks.
