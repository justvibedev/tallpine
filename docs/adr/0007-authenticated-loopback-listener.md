# 7. Authenticate the loopback listener with a per-install token

Date: 2026-10-25
Status: Accepted

## Context

The daemon's primary listener binds `127.0.0.1` and, until now, trusted every
caller that could reach it. Loopback is not a principal: other OS users,
Docker Desktop containers reaching the host, sandboxed tools, and Tallpine's own
agents all share it. Any of them could spawn a `bypass-permissions` session,
type into agents, open a raw shell terminal, or resolve a pending approval.
The browser boundary (exact-Origin CORS, the loopback `Host` allowlist) stops
web pages, but not local processes. AGENTS.md made "the loopback listener
stays unauthenticated" a hard rule, so this change replaces that rule.

## Decision

Every request to the loopback listener, HTTP and WebSocket alike, must carry
the daemon's per-install token in the `X-VO-Token` header. A missing or wrong
token gets `401 DAEMON_TOKEN_REQUIRED` with a message that names the header
and the token file. The comparison is constant time.

- **Secret.** 32 random bytes, base64url, in `daemon.token` next to
  `running.json` (`~/.tallpine/daemon.token` by default). The run-file directory is
  the one location the daemon, the CLI, and Electron already agree on, and it
  moves with `TALLPINE_RUN_FILE` and the `TALLPINE_DATA_DIR`-based dev and lab isolation.
  A state-dir location would split the desktop daemon, which always runs with
  `TALLPINE_DATA_DIR=~/.tallpine/data`, from a plain-shell CLI. The daemon creates the file
  with mode `0600` (set explicitly, not left to the umask) via an atomic
  temp-file publish, regenerates it when it is missing, malformed, or readable
  by others, and restores it if it is deleted while the daemon runs. It
  persists across restarts so long-lived agent shells stay valid. It is never
  written to `running.json`, and it is never accepted from a query string,
  which access logs record.
- **CLI.** The `tallpine` client reads the file on every daemon call. Hook commands
  (`tallpine hooks ...`) and the `tallpine` calls agents make go through the same client,
  as the same user with the same `TALLPINE_RUN_FILE`.
- **Electron.** Main reads the file and injects the header through
  `session.webRequest.onBeforeSendHeaders` for the daemon origin (REST, SSE,
  media, and the `/mux` WebSocket upgrade), and wraps main's Node `fetch`. The
  renderer never holds the token. Agent pages framed in the chat get it only
  for requests on their own files, so an agent page cannot reach the API.
  WebSockets authenticate with the header that Electron injects. No in-repo
  client needs the `Sec-WebSocket-Protocol` fallback, so the daemon does not
  accept the token there.
- **Exemptions** (the full list, in `daemonAuthExempt`): `GET`/`HEAD`
  `/healthz` and `/readyz`; CORS preflights, which `corsMiddleware` answers
  without reaching a handler; requests on preview and artifact hosts, which
  `previewOriginMiddleware` serves only as file bytes; `GET`/`HEAD` preview file
  and render document paths, which browser panels, iframes, and the render check
  load as sandboxed documents. (A Codewhale lifecycle webhook exemption was
  removed: no handler was mounted there.) Any new exemption needs an update
  to this ADR.
- **LAN listener.** Unchanged. It keeps its bearer-password `authMiddleware`
  and ignores `X-VO-Token`.
- **Rollout.** Enforced by default, because this is a single-user fork with a
  bundled CLI. `TALLPINE_DAEMON_AUTH=off` on the daemon disables enforcement, for
  debugging only, and only together with `TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1` in an
  unstamped dev build. Version-stamped release builds ignore both flags (auth
  stays on), and so does a dev build with only the first; the daemon logs a
  warning.
  The daemon also logs a warning when auth is off.

## Consequences

- A local process now needs read access to the user's `daemon.token` to drive
  the daemon. Other OS users and containers without that file are refused.
- Processes running as the same user, agents included, can still read the
  file and act with full daemon authority. The token stops cross-user and
  cross-container access, not a same-user agent escalating its own
  permissions.
- Third-party scripts and the frozen npm `0.10.0` CLI that call the API
  without the header get a 401 that tells them what to send.
- Preview and artifact file reads remain unauthenticated for anyone who knows
  a session id. They are scoped, read-only, and were already outside the
  token's reach for browser tabs. Without the token they never serve a credential-bearing file
  (`previewutil.IsSecretPath`): anything under `.git`, `.ssh`, `.aws`,
  `.gnupg`, `.kube`, `.azure` or `.tallpine`, or under `.tallpine` other than
  `.tallpine/attachments`; `.env` and `.env.*` other than shared
  templates (`.env.example`, `.env.sample`, `.env.template`, `.env.dist`);
  `.npmrc`, `.netrc`, `.pypirc`, `.yarnrc.yml`, `.pgpass`, `.git-credentials`,
  `.htpasswd`, `.dockercfg`; SSH private keys; `.docker/config.json`; and
  `*.pem`, `*.key`, `*.p12`, `*.pfx` and Terraform state. Such a read gets the
  missing-file 404. Ordinary dotfiles (`.github`, `.claude`, `.storybook`,
  `.tallpine/attachments`) stay readable, because the Browser tab opens
  workspace-file links through this path without the token. The check applies
  to the URL and again to the resolved file path, since a preview origin maps
  the URL under its stored entry's directory. The renderer, framed agent pages
  reading their own files, and LAN clients are authenticated and unaffected.

## Follow-ups

- **Per-session scoped agent tokens.** Done in
  [ADR 0008](0008-scoped-agent-session-tokens.md): agents get
  `TALLPINE_SESSION_TOKEN`, a session-bound credential authorized against a
  deny-by-default capability policy.
- `tallpine daemon rotate-token`, and preview capability tokens for browser tabs.
