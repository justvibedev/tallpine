# AGENTS.md

> This repository is **Tallpine**, a hard fork of [Agent Orchestrator](https://github.com/OrchestratorInc/agent-orchestrator). See `README.md` and `NOTICE`.

Operational guidance for coding agents working in this repository. Keep changes small, match the current rewrite architecture, and prefer the documented daemon/API boundaries over behavior from the old TypeScript implementation.

Follow [CONTRIBUTING.md](CONTRIBUTING.md) before choosing an issue or starting work.

## Repo layout

- `backend/`: Go rewrite of Tallpine with the Cobra `tallpine` CLI, loopback HTTP daemon, services, SQLite storage, lifecycle/reaper, runtime/workspace/agent/tracker adapters, terminal mux, and tests.
- `frontend/`: Electron + React supervisor wired to the daemon through the generated typed client. Treat it as a thin supervisor/UI surface; do not move daemon logic into it.
- `docs/`: Current architecture and status notes. Start here before changing lifecycle, CLI, agents, storage, or daemon behavior.
- `test/`: External smoke/e2e assets, including the CLI fresh-install container check.
- `.github/workflows/`: the old CI definitions, kept for reference. **GitHub Actions is disabled for this repo; nothing runs remotely.** `scripts/verify` runs the same checks locally.

## Commands

GitHub Actions is disabled: there is no remote CI, and local verification is the merge gate. One entry point, from the repo root:

```bash
make verify-fast      # = scripts/verify --changed: only what the branch touches vs origin/main (run while iterating and before every push)
make verify           # = scripts/verify --full: everything the old CI workflows ran (run once before handing over a PR)
make lint-changed     # = scripts/verify --lint-changed: the same checks restricted to files changed vs origin/main (workers)
make lint             # = scripts/verify --lint: gofmt + golangci-lint v2.15.0 (repo config, goimports formatter, all packages): exactly the merge gate's go-fmt + go-lint jobs Also always runs the repo-scanning Go invariant tests (brand, privacyguard); TS-only or other-package changes cannot skip them.
```

`--changed` picks steps from the diff against the merge-base with `origin/main` plus untracked files: gofmt on changed Go files; build and vet; `go test` for the changed packages; golangci-lint `--new-from-rev`; sqlc or API drift when their sources changed; frontend typecheck (tsgo, `typecheck:fast`, when installed), e2e typecheck and `vitest --changed` for frontend changes; product-ui and docs checks for those trees; gitleaks on the branch's commits. `--full` adds the whole Go suite, the `-tags e2e` CLI suite, the authoritative `tsc` 5.9 typecheck, all of vitest and the docs build. Options: `--race`, `--base <ref>`, `--no-retry`. A failed Go package or vitest file is re-run once and reported as `flaky` if it then passes. Every step runs; the summary lists failures, skips and flakes, and the exit status is non-zero on any failure. `--changed` tests only the changed packages, not their dependents, so run `make verify` before handing over a PR that changes shared packages.

**Workers: run `make lint-changed` (= `scripts/verify --lint-changed`) while iterating and before each push, never the full `make lint`** (main's local CI runs the full lint after every merge). It checks only what changed vs origin/main: gofmt on changed Go files, golangci-lint `--new-from-rev` on changed packages, `size-ratchet --changed`, the repo-scanning invariants, and the frontend typecheck when frontend files changed.

**Full `make lint` is for the orchestrator, the merge gate and main's CI only; workers use `make lint-changed`.** Hand-run `golangci-lint` on a few packages skips the gofmt step and the other packages, and a result from before your last edit proves nothing. Run `make lint-changed` after the final edit, before every push; a goimports or gofmt finding fails it, same as the merge gate.

Other commands:

```bash
npm run test:go                      # backend go test -count=1 ./...
npm run lint                         # golangci-lint only, NOT the pre-push check (no gofmt step); use make lint-changed
npm run frontend:typecheck           # frontend TypeScript check (tsc 5.9, authoritative; what verify runs)
npm run frontend:typecheck:fast      # same check on the native TypeScript 7 compiler (tsgo), incremental; seconds instead of minutes
npm run sqlc                         # regenerate backend/internal/storage/sqlite/gen from queries/schema
npm run api                          # regenerate OpenAPI spec + frontend TS types (see API contract changes below)
```

### Toolchain

- **Go:** use the pinned toolchain, never a private copy in `/tmp` or Go in Docker. `scripts/go` installs Go 1.27.3 (the `go` line in `backend/go.mod`) once per user, checksum-verified, into `~/Library/Caches/vo-dev/toolchains/` (Linux: `~/.cache/vo-dev/toolchains/`), and every checkout and worktree reuses it. Run `scripts/go <args>` or put the shims first on `PATH` from the repo root: `export PATH="$PWD/scripts/bin:$PATH"` (direnv users get this from `.envrc`). `GOCACHE`, `GOMODCACHE` and the npm cache are one machine-wide copy per user: `scripts/go` resolves them from the account's real home even under `env -i HOME=$(mktemp -d)`, and `testenv` pins them before it moves `HOME`; never point them inside a scratch HOME or a worktree. The daemon's daily cleanup (`go_build_cache` category, on with the harness caches switch) removes Go build cache entries unused for 2 days, so the cache cannot grow without bound across worktrees; session scratch (`$TMPDIR` under the data dir) is removed when the session is killed or archived.
- **Node:** Node 24 and npm. New session worktrees get `node_modules/` from the main checkout through the tracked `.worktreeinclude` (see "Desktop lab" below), so run `npm install --prefer-offline` to reconcile instead of a fresh `npm ci`.

Backend-specific checks:

```bash
cd backend
go build ./...
go test ./...
go test -race ./...
go vet ./...
TALLPINE_DATA_DIR=/tmp/tallpine-dev/data TALLPINE_RUN_FILE=/tmp/tallpine-dev/running.json TALLPINE_PORT=3199 go run ./cmd/tallpine daemon  # scratch daemon
```

`tallpine start` opens the installed desktop app, which owns the human's real `~/.tallpine`; it is not a backend check. Inside an agent session (`TALLPINE_SESSION_ID` or official `AO_SESSION_ID` set), `tallpine start` refuses, and `tallpine daemon` refuses a data dir or run file under `~/.tallpine` outside `~/.tallpine/dev/`. Set `TALLPINE_DEV_REAL_DATA=1` only when you really mean the real state.

Frontend-specific checks:

```bash
cd frontend
npm run typecheck:fast      # iterate with this: tsgo (TypeScript 7), ~10 s cold, ~2 s after a leaf edit
npm run typecheck:changed   # authoritative tsc, only the projects your branch and working tree affect
npm run typecheck           # authoritative tsc on every project (tsc -b, incremental); run before pushing
npm run build
```

#### TypeScript projects

`frontend/tsconfig.json` is a solution file. `tsc -b` builds its projects in dependency order: `shared` (`src/shared`, `src/api`, `packages/shared`) → `main` (`src/main.ts`, `src/main/`) → `preload` (`src/preload.ts`, `src/annotate-preload.ts`) → `renderer` (`src/renderer/`, `packages/product-ui/src`) → `test` (every `*.test.ts(x)` and `src/renderer/test/`), plus `tooling` (Forge/Vite/Playwright configs). Shared compiler options live in `tsconfig.base.json`.

- A project reads its references' declarations, not their sources. A body-only edit re-checks one project; a test-only edit never re-checks app code.
- Build info and declarations go to `frontend/node_modules/.cache/tsc/` (`typecheck:fast` uses `.cache/tsgo/` and its own single-program `tsconfig.fast.json`). Delete the directory if a build ever looks stale.
- A new source file must live under a project's `include`. Otherwise `tsc -b` fails with TS6307 ("not listed within the file list of project").
- `vitest/globals` types exist only in the `test` project. App code cannot call `describe`/`vi` without importing them.

When showing or demoing frontend changes, run `tallpine preview [url]` from inside the session so the change renders in the desktop browser panel (the inspector rail's Browser tab); do not just describe it.

### Desktop lab (running the real Electron app for UI review)

For visual verification that needs the real app rather than `tallpine preview` or `dev:web`, run the checkout in an isolated worktree against scratch data. Never use the invoking working tree (it may hold unrelated uncommitted work) and never touch the user's real `~/.tallpine` data.

```bash
git worktree add /tmp/tallpine-lab <branch>   # the PR branch under review
cd /tmp/tallpine-lab/frontend
npm ci                                  # never symlink node_modules from another checkout (see below)
npm run build:daemon -- --dev
TALLPINE_DATA_DIR=/tmp/tallpine-lab-data ./node_modules/.bin/electron-forge start
```

- **Run a real `npm ci`.** Symlinking another checkout's `node_modules` makes React resolve from two physical paths and shares the Vite optimizer cache across checkouts: the symptom is a black window with `Invalid hook call` / `useSyncExternalStore` errors in the renderer console. It also writes `.vite/deps` output into the other checkout's tree.
- **A copy-on-write clone is not a symlink.** The rule above forbids sharing physical paths. A `clonefile(2)` copy of `node_modules` has its own paths and is fine. Tallpine does this for you: the repo's tracked `.worktreeinclude` lists `node_modules/`; enable the project's `cloneWorktreeInclude`, with `cd frontend && npm install --prefer-offline` as a `postCreate` step to reconcile the lockfile. See "Clone large directories instead of copying them" in `frontend/src/docs/content/configuration/projects.mdx`.
- **Inside an agent session, dev builds isolate themselves.** `npm run dev` / `electron-forge start` started with `TALLPINE_SESSION_ID` or `AO_SESSION_ID` set drops the inherited production wiring (`TALLPINE_DATA_DIR`, `TALLPINE_RUN_FILE`, `TALLPINE_TMUX_SOCKET_NAME`, `TALLPINE_SESSION_TOKEN`, …) and uses `~/.tallpine/dev/checkouts/<hash of the checkout>/` for data, run file and Electron profile, plus a per-checkout daemon port in 3200-3699. It logs the chosen paths at startup. An explicit `TALLPINE_DATA_DIR`/`TALLPINE_RUN_FILE` outside `~/.tallpine` (or under `~/.tallpine/dev`), a non-production `TALLPINE_PORT`, and `TALLPINE_DEV_*` knobs are kept. `TALLPINE_DEV_REAL_DATA=1` turns this off.
- **Isolate data with `TALLPINE_DATA_DIR`.** The lab starts with an empty board; create throwaway sessions inside it to exercise list/detail flows. The Electron `userData` dir already defaults to a dev path, not the production one.
- **Diagnose with `ELECTRON_ENABLE_LOGGING=1`.** Renderer console errors (the cause of most blank windows) land in the launcher's stdout.
- **Killing the forge wrapper is not enough.** The Electron main process cmdline is just `Electron .`, so `pkill -f electron-forge` leaves the app running and the next launch exits silently. Kill the `Electron.app/.../MacOS/Electron` main process explicitly before relaunching.
- A missing-dependency warning for a lazily imported module (for example `mermaid`, which is dynamically imported on first use) does not blank the window; treat it as informational.

## Where to look first

- `README.md`: Current run/config/test quickstart.
- `docs/README.md`: Docs index.
- `docs/documentation-map.md`: Which artifacts are the machine-readable contract layer (`openapi.yaml`, `AGENTS.md`, `skills/`, sqlc `gen/`), what each is source of truth for, and how CI keeps them from drifting.
- `docs/architecture.md`: Backend mental model, package layout, lifecycle/session/service boundaries, and load-bearing rules.
- `docs/cli/README.md`: Intended CLI shape, a thin Cobra client over daemon HTTP with no direct storage/runtime access.
- `CLAUDE.md`: Compatibility pointer for Claude Code. It directs agents back to `AGENTS.md`.

For code entry points:

- CLI commands: `backend/internal/cli/*.go`; follow nearby command/test patterns before adding a new style.
- HTTP controllers and DTOs: `backend/internal/httpd/controllers/`.
- Service read/write boundaries: `backend/internal/service/`.
- Domain vocabulary: `backend/internal/domain/`.
- Port contracts: `backend/internal/ports/`.
- SQLite queries/migrations/store: `backend/internal/storage/sqlite/`.
- Generated sqlc code: `backend/internal/storage/sqlite/gen/`.

## Distribution

- This repository is a hard fork with no upstream release channel and no version-floor check. The macOS desktop app has one updater (`frontend/src/main/auto-updater.ts`): it reads GitHub Releases on `justvibedev/tallpine` (`latest-mac.yml`, zip, blockmap) and accepts only builds signed by the release Team ID (injected at build time from the signing identity); it is inert on unpackaged, dirty or local builds. Releases are published only with `scripts/release-local publish <app> --publish-github` from a notarized build.
- Do not add update telemetry, and do not add code that phones home to any other release, update, or version endpoint.

## Coding conventions

- Keep every change surgical and directly tied to the task. Avoid drive-by cleanup, broad renames, formatting churn, speculative abstractions, and architectural refactors unless the task explicitly asks for them.
- Follow existing Go package boundaries. CLI code should call daemon HTTP routes through shared CLI client helpers; it should not open SQLite, spawn runtimes, or call adapters directly.
- Keep Cobra commands in the relevant command file and table-test them in the style of `backend/internal/cli/*_test.go`.
- Mirror existing response/request DTOs in the CLI instead of importing HTTP controller packages into CLI code, unless the package already establishes that dependency.
- Return usage errors as `usageError` so CLI misuse exits 2; runtime/daemon failures should exit 1.
- Preserve API error envelopes and request IDs when surfacing daemon errors.
- Use `context.Context` as the first argument for functions that do I/O or blocking work.
- Do not add abstractions for one-off use cases. Add helpers only when they remove duplication across real call sites.
- Tests should cover the user-visible behavior and boundary being changed: happy path, validation/missing args, daemon error envelopes, and any destructive confirmation path.

## Hard rules and boundaries

- The daemon's **primary (loopback) listener** stays bound to `127.0.0.1` and is **authenticated**: every request must carry the per-install token from `daemon.token` (next to `running.json`, mode 0600) in the `X-VO-Token` header, compared in constant time; `daemonAuthMiddleware` (`backend/internal/httpd/daemon_auth.go`) answers anything else with 401 `DAEMON_TOKEN_REQUIRED`. Do not change its bind host, do not accept the token from query strings, and do not expose the token to the renderer (Electron main injects it; the CLI reads the file). The exemptions are the closed list in `daemonAuthExempt`; adding one needs an update to `docs/adr/0007-authenticated-loopback-listener.md`. `TALLPINE_DAEMON_AUTH=off` exists for debugging only and is ignored unless `TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1` is also set and the build is an unstamped dev build (release builds ignore both). Every request on it must carry a loopback `Host` (`127.0.0.1`, `localhost`, `[::1]`, or a `*.localhost` preview host); `hostAllowlistMiddleware` (`backend/internal/httpd/host_allowlist.go`) answers anything else with 403 `HOST_FORBIDDEN` before any handler, which blocks DNS-rebinding reads. LAN-listener requests are exempt (they use bearer auth). Any new in-repo client must address the daemon as `127.0.0.1:<port>` and send `X-VO-Token`.
- **Agents never get the install token.** Every agent launch gets a scoped `TALLPINE_SESSION_TOKEN` (`backend/internal/sessiontoken`), minted only in the launch path (`launchRuntimeEnv`, the reviewer launcher) and sent by the CLI in place of `daemon.token`. The daemon authorizes it against `agentRules` (`backend/internal/httpd/agent_scope.go`), a deny-by-default allowlist: workers act on their own session, orchestrators on their own project, reviewers on their own review. No agent may resolve approvals or inputs, change settings or project config (the opt-in `crossProjectOrchestration` policy lets orchestrators manage projects they created; see ADR 0008), or reach daemon control, mobile, or shell terminals. Handlers whose scope is in the body or query enforce it with `requestscope.AgentFrom`. Adding a rule, or a handler that an allowed rule reaches with a body-level target, needs an update to `docs/adr/0008-scoped-agent-session-tokens.md` and a boundary test. Never put the install token, or its value, in an agent's environment.
- The daemon MAY run a **second, opt-in LAN listener** (the "Connect Mobile" feature) that binds `0.0.0.0` **only while explicitly enabled**, **only** behind the bearer-password `authMiddleware`, serving the app API but never the loopback-gated control routes (`/shutdown`, mobile control). **Exactly one route is exempt from `authMiddleware`: `GET /api/v1/identity`**, which returns an opaque host id and the mobile contract version so a phone can confirm which machine answered before presenting a credential. See `docs/adr/0003-unauthenticated-identity-probe.md`. The exemption is an exact path, `GET` only, and checked ahead of the lockout; any further unauthenticated route needs its own ADR. The direct LAN listener is plaintext for trusted home networks. See `docs/adr/0001-lan-listener-for-mobile.md` and `CONTEXT.md` for the original decision. Current managed cloudflared and Tailscale TLS endpoints wrap this authenticated mobile path; they do not authorize exposing the primary listener or adding an unauthenticated route. See `frontend/src/docs/content/configuration/remote-access.mdx` for current pairing behavior. Do not add any other network-facing bind.
- The CLI is a thin client. Do not port old in-process TypeScript CLI behavior that bypasses daemon HTTP routes.
- Do not store derived/display session status. Status is derived from durable facts (`activity_state`, `is_terminated`, PR/check/comment facts) at service read time.
- Do not treat failed/unknown runtime probes as proof a session is dead.
- Do not force-delete dirty registered worktrees.
- Do not modify already-merged SQLite migrations. Add a new migration instead.
- Create new SQLite migrations with `npm run migration:create -- <snake_case_name>` (UTC `YYYYMMDDHHMMSSsss` IDs); append the generated ID and filename to `shippedMigrations` in `migrate_burned_versions_test.go`.
- Do not hand-edit `backend/internal/storage/sqlite/gen/*`; change `backend/internal/storage/sqlite/queries/*` or migrations and run `npm run sqlc`.
- SQLite change events come from DB triggers into `change_log`; do not add parallel manual CDC emission from store methods unless the architecture changes explicitly.
- Keep generated OpenAPI/API DTO drift in mind: controller response shapes live in `backend/internal/httpd/controllers/dto_*.go` and tests may assert CLI/HTTP wire compatibility.
- Do not add network calls to tests unless the package already has an integration/e2e pattern for them. Prefer `httptest`, fakes, and injected dependencies.
- **Tests are hermetic to the agent session.** Every backend test package has a `TestMain` that calls `testenv.Main(m)` (`backend/internal/testenv`; a new package needs a `testmain_test.go`, and `TestTestMainsUseTestenv` fails without it). It unsets inherited `TALLPINE_*`/`AO_*`/`CLAUDE*`/`CODEX_*`/`TMUX*`/`GH_*`/`GIT_*` variables, gives the binary a sandbox `HOME`/XDG dirs/`TMUX_TMPDIR`, sets `HISTFILE=/dev/null`, and puts failing `gh`/`glab` shims first on `PATH`. Tests must not read or write the real home directory or reach GitHub; put a fake tool earlier on `PATH` instead. Live-provider opt-ins (`TALLPINE_LIVE_*`, `TALLPINE_CODEX_LIVE`) and `TALLPINE_TEST_*` helper signals are kept. Vitest does the same in `frontend/src/renderer/test/hermetic-env.ts`.
- Do not commit local run state, daemon data, temporary worktrees, build outputs, or credentials.
- **Side-by-side identity (Tallpine).** This hard fork must keep running next to an official Agent Orchestrator install on the same Mac. Every identifier that could collide lives in one place per side: `backend/internal/brand/brand.go` (Go) and `frontend/src/shared/brand.ts` (Electron/renderer), kept in sync by their tests. Product `Tallpine`; CLI binary `tallpine` (no `ao` alias); bundle/app id `ee.justdev.tallpine`; state root `~/.tallpine` (Electron `userData` `~/.tallpine/electron`); env prefix `TALLPINE_*` (`TALLPINE_DATA_DIR`, `TALLPINE_RUN_FILE`, `TALLPINE_PORT`, and the agent-session variables such as `TALLPINE_SESSION_ID`); default daemon port `3101`; URL scheme `tallpine://`, in-app links `tallpine://sessions/...`; tmux socket `tallpine`; launchd labels `ee.justdev.tallpine.*`; MCP server name `tallpine`; skill `using-tallpine`. Read these from the brand modules; never hardcode `ao`, `.ao`, `AO_*`, `3001`, `ao-app`, or `com.aoagents.*` again, and never write to the official install's files. Hook installers write `tallpine hooks <agent> <event>` entries (prefix `tallpine hooks `) and must not modify or remove the official `ao hooks` entries. Two names stay for compatibility: Tallpine writes the per-workspace `.tallpine/launch.json` and `.tallpine/attachments/` and still reads the older `.ao/launch.json` and `.ao/attachments/` as a fallback, and (read-only, for sessions created before the rebrand) the legacy `ao/<session-id>` git branch namespace is still recognised; new session branches are `tp/<session-id>/root`.
- **No telemetry.** This fork ships no analytics, product telemetry, or crash reporting: no PostHog, Sentry, or equivalent SDK, endpoint, baked key/DSN, or consent/opt-out UI for one. Do not add one without an explicit human decision. `backend/internal/privacyguard` fails `go test ./...` (and so `npm run lint`) if such an SDK appears in an import or a dependency manifest. Local logs and the usage/cost views fed by agent output are not telemetry.
- All Tallpine-owned app state lives under `~/.tallpine` only. The daemon's data dir, `running.json`, worktrees, and the Electron supervisor's `userData` (Chromium cache, cookies, local/session storage, crash dumps) must resolve under `~/.tallpine` (overridable via `TALLPINE_DATA_DIR`/`TALLPINE_RUN_FILE`). Never write to or otherwise use `~/Library/Application Support` or any other OS default app-data location for Tallpine state. The sole read exception is an explicit, user-initiated browser-profile import: it may read only validated, known source-browser profile files, must never write to the source profile, must keep source paths out of the renderer, and must place every snapshot, staging file, and imported result under `~/.tallpine`. The second read exception is the explicit, user-initiated `tallpine import ao` / "Import from Agent Orchestrator" migration (`backend/internal/officialimport`): it may read only the official install's `ao.db` (and its `-wal`), and only through a copy-on-write snapshot (`cp -c`) placed under `<data dir>/import-staging`; it must never open, lock, write or migrate the official database in place, and never reads tokens, secrets, sessions, chat or browser data. The official data dir name comes from `brand.OfficialHomeDirName`. `main.ts` pins Electron's `userData` to `~/.tallpine/electron`; do not remove that override or rely on Electron's default path.

## API contract changes

The daemon API is code-first. The OpenAPI spec and frontend TypeScript types are generated artifacts. Edit the source, then regenerate.

**Source files to edit:**

- `backend/internal/httpd/controllers/dto_*.go`: request/response shapes.
- `backend/internal/httpd/apispec/specgen/build.go`: operation registry. Add a `schemaNames` entry for any new named type.

**Regenerate after editing:**

```bash
npm run api          # runs api:spec then api:ts in sequence
```

This is equivalent to running:

```bash
npm run api:spec     # cd backend && go generate ./internal/httpd/apispec/...
npm run api:ts       # npx openapi-typescript@7.4.4 backend/internal/httpd/apispec/openapi.yaml -o packages/shared/api/schema.ts
```

**Verify:**

```bash
cd backend && go test ./internal/httpd/...    # spec drift + route/spec parity tests (does not cover schema.ts; scripts/verify's api-drift step checks it)
```

Commit `openapi.yaml` and `packages/shared/api/schema.ts` together with the Go changes. `scripts/verify` regenerates both files when their sources change (`--full`: always) and fails if the committed versions are out of date. The CLI hand-mirrored DTOs remain a deliberate manual boundary and are not generated.

## PR hygiene

- When creating or updating a PR, follow `.agents/skills/pr-description/SKILL.md` so the published description starts with verified category change counts.

- Before creating or handing over a PR, run `make verify` on the final commit and fix what it reports; run `make verify-fast` before every push while iterating. There is no remote CI to wait for or re-check: do not ask for one, and do not enable GitHub Actions. If a step cannot run locally (for example a Windows-only test), say so in the PR body. Report the summary block (steps, times, any `flaky` lines) in the PR's tests section. A step that fails on `origin/main` too is main debt: name it in the PR instead of fixing unrelated code.

- Branch from `main` unless explicitly continuing an existing PR.
- Keep one issue per PR. If asked for separate work, create a separate branch and PR.
- Use conventional commit messages (`feat:`, `fix:`, `docs:`, `test:`, `chore:`).
- Explain intentional omissions in the PR body, especially when the TypeScript original had more behavior than the Go rewrite domain currently supports.
- Run the narrowest relevant tests first (one package, one test file), then `make verify-fast`, then `make verify` once before handover.
