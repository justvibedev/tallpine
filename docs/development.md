# Development guide

How to set up, build, run, and test Tallpine locally.

## Prerequisites

| Tool       | Minimum version | Notes                                                                  |
| ---------- | --------------- | ---------------------------------------------------------------------- |
| Go         | 1.27.3          | `scripts/go` installs the pinned toolchain once into the user cache; `export PATH="$PWD/scripts/bin:$PATH"` |
| Node.js    | 24         | `node --version`; install via [nodejs.org](https://nodejs.org/)        |
| npm        | 10              | Ships with Node.js                                                     |

Additional runtime dependencies for the daemon:

- **git** (for worktree creation and agent integration)
- **A running agent CLI** (Claude Code, Codex, or Kiro). See [the installation
  guide](https://docs.tallpine.eu/installation).

For source terminal runs on macOS, install/configure tmux; packaged builds
bundle it. New macOS/Linux sessions use detached native PTY hosts, while old
handles and host-start fallback use tmux. `tallpine doctor` still checks tmux on both.

## Project layout

```text
tallpine/
  backend/              # Go daemon (Cobra CLI, HTTP API, services, storage)
    cmd/tallpine/             # CLI entry point
    internal/           # All library code
      cli/              # CLI command implementations
      httpd/            # HTTP controllers, apispec, middleware
      service/          # Business logic layer
      domain/           # Domain types
      ports/            # Port interfaces (contracts)
      storage/          # SQLite migrations, queries, generated code
  frontend/             # Electron + React desktop app
    src/                # Renderer, main, preload
    e2e/                # Playwright end-to-end tests
  packages/
    mobile/             # React Native (Expo) mobile companion app
    tp/                 # Legacy npm CLI package (frozen)
  docs/                 # Architecture, ADRs, CLI docs, status
  CONTRIBUTING.md       # Contribution guide
```

## Getting the code

```bash
git clone https://github.com/justvibedev/tallpine.git
cd tallpine
npm ci
```

### Branching

```bash
git checkout -b tp/my-feature-branch origin/main
```

Keep your branch up to date by rebasing on main:

```bash
git fetch origin
git rebase origin/main
```

### Committing

Keep commits atomic. Use one logical change per commit. Stage related changes and
commit with a conventional message:

```bash
git add <files>
```

Commit message tags:

| Tag        | When to use                           |
| ---------- | ------------------------------------- |
| `feat`     | New feature                           |
| `fix`      | Bug fix                               |
| `docs`     | Documentation only                    |
| `test`     | Adding or fixing tests                |
| `refactor` | Code change with no functional change |
| `chore`    | Maintenance, tooling, dependencies    |

Use **trailers** to provide additional context:

```bash
git commit -m "fix: handle nil pointer in session lookup

The session resolver panicked when the store returned a nil session
without an error. Return ErrNotFound instead.

Signed-off-by: Your Name <your.email@example.com>
Co-authored-by: Contributor Name <contributor@example.com>"
```

## Backend

### Build

```bash
cd backend
go build ./...
```

### Run the daemon

```bash
cd backend
# Start the daemon (loopback HTTP server on 127.0.0.1)
go run ./cmd/tallpine daemon
```

The CLI is built with Cobra. From `backend/`, run `go run ./cmd/tallpine --help` for
available commands.

### Run tests

```bash
cd backend
go test ./...              # all tests
go test -race ./...        # with race detection
go test -v ./internal/cli/ # a specific package
```

### Lint

```bash
make lint   # gofmt + golangci-lint: the full check, same as the merge gate
make lint-changed   # same checks, only for what this branch changed vs origin/main (fast pre-push)
```

### Code generation

Create new SQLite migrations from the repository root:

```bash
npm run migration:create -- add_reports
```

The command creates a Goose SQL template with a UTC `YYYYMMDDHHMMSSsss` ID.
Fill in its SQL and append the generated ID and filename to `shippedMigrations`
in `backend/internal/storage/sqlite/migrate_burned_versions_test.go`.
Keep existing migration IDs, filenames, and SQL unchanged.

```bash
# Regenerate sqlc code after editing queries or schema
npm run sqlc

# Regenerate OpenAPI spec and frontend TypeScript types
npm run api
```

## Frontend

### Install dependencies

```bash
cd frontend
npm install
```

### Run in development mode

```bash
cd frontend
npm run dev            # Electron dev mode
npm run dev:web        # Web-only (no Electron, for quick UI iteration)
```

### Build

```bash
cd frontend
npm run package        # Package for current platform (macOS without signing: TALLPINE_ALLOW_UNSIGNED=1 npm run package)
npm run make           # Create distributables when platform packaging deps are installed (same flag for unsigned macOS)
```

A packaged macOS build launched outside `/Applications` moves itself into
`/Applications` when nothing is installed there. For smoke tests always launch
with `TALLPINE_DISABLE_RELOCATION=1` and a scratch `HOME` and `TALLPINE_DATA_DIR`; the app
also skips relocation for a non-default `TALLPINE_DATA_DIR`/`TALLPINE_RUN_FILE` or a bundle
under `~/.tallpine` or `frontend/out/`.

Before spawning the bundled daemon, wake-helper, cloudflared and the ACP Node
runtime, a packaged, Developer-ID-signed macOS build verifies each binary with
`codesign --verify --strict -R` against `anchor apple generic and certificate
leaf[subject.OU] = "<team id>"` (team id baked in from the signing identity;
`frontend/src/main/helper-signature.ts`). On failure the app refuses to start the
daemon and shows the path and a reinstall hint. Dev and unsigned builds skip the
check, and say so in the log. Results are cached by inode, mtime and size. Install
in `/Applications` or `~/Applications` so other users and processes cannot write
into the bundle.

On a fresh Linux machine, treat `npm run package` as the default local build
path. `npm run make` also needs Linux packaging tools that are not provided by a
minimal setup or by `nix develop` today:

- `rpm` / `rpmbuild` for the RPM target
- the usual distro packaging toolchain required by Electron Forge makers

CI installs `rpm` explicitly before running `npm run make`. Do the same locally
if you need Linux distributables, or skip `npm run make` on a fresh setup.

### Run tests

```bash
cd frontend
npm run test           # Vitest unit tests in a simulated renderer environment
npm run test:e2e       # Playwright browser-based renderer E2E tests
npx playwright show-report  # View Playwright report
```

### Typecheck

```bash
cd frontend
npm run typecheck
```

Or from repo root:

```bash
npm run frontend:typecheck
```

## Mobile companion app

The mobile companion app is still being wired into the contributor docs. Do not
assume `packages/mobile/README.md` is a complete setup guide on this branch.
Until a tracked guide lands, use the desktop/backend workflow above and check
open issues/PRs for current mobile-specific setup notes.

## Running end-to-end

1. Start the desktop app with `npm run dev` from `frontend/`.
2. The Electron main process starts and supervises the loopback daemon for you.
3. Use `npm run dev:web` only for renderer-only development; it does not launch Electron.

For CLI-only usage, open two terminals:

**Terminal 1: start the daemon.**

```bash
cd backend
go run ./cmd/tallpine daemon
```

**Terminal 2: interact while the daemon is running.**

```bash
cd backend
go run ./cmd/tallpine status
go run ./cmd/tallpine --help
```

## Security scanning

`scripts/security-scan` runs free, local, report-only scanners and writes each tool's raw output plus a one-line-per-tool `summary.txt` to a results directory (default `$TMPDIR/vo-security-scan-<timestamp>`, never inside the repo):

```bash
scripts/security-scan                                  # repo only
scripts/security-scan /tmp/scan --app "$HOME/Applications/Tallpine.app"   # also scan the built app and read its Electron fuses
scripts/security-scan --strict                         # exit 1 on reachable govulncheck vulns or osv high/critical
scripts/security-scan --only govulncheck,gosec         # a subset: govulncheck, osv, gosec, semgrep, trivy, fuses
```

It runs govulncheck and gosec on `backend/` (via the pinned `scripts/go`), osv-scanner on every tracked lockfile (against locally downloaded advisory data), Semgrep CE from a pinned Docker image with free registry rulesets (Go, TypeScript, JavaScript, Node.js; no login, `--metrics=off`), and Trivy for a CycloneDX SBOM and vulnerability counts of the repo and, with `--app`, the app bundle. `--app` also prints the app's Electron fuses (read-only, via a pinned `@electron/fuses`). Exit status is 0 unless `--strict`. Nothing is uploaded; the tools only download public rules and advisory data. Host needs `jq`, `docker`, `osv-scanner`, `trivy` and `npx`. Tool versions are pinned in `scripts/security-scan.d/*.sh`; `scripts/security-scan.test.sh` tests the parsers and flags.

Remaining advisory matches and why they do not apply are recorded in [dependency-advisory-triage.md](dependency-advisory-triage.md).

## Testing tips

### Backend

- Backend tests use `httptest.Server` and injected fakes. They do not require a
  real daemon.
- Run the narrowest relevant test suite first (e.g. `go test ./internal/cli/`),
  then the full suite.

### Frontend

- Unit tests use Vitest and run in a simulated renderer environment.
- E2E tests use Playwright against the web renderer started by `npm run dev:web`;
  they do not launch the full Electron app.
- After changing API types, run `npm run api` from root to regenerate
  `packages/shared/api/schema.ts`.

## Releasing to your own machine (macOS)

`scripts/release-local` upgrades the app you use every day
(`~/Applications/Tallpine.app`, data in `~/.tallpine/data`) to a known-good
commit and rolls it back with one command. Run `install` and `rollback` from a
plain terminal, not from an agent session: they restart the daemon that hosts
every session. The script refuses to do that from inside a session unless you
pass `--allow-in-session`.

```bash
scripts/release-local status              # installed commit vs origin/main, CI rows, rollback points
scripts/release-local build               # build origin/main (or: build <ref>)
scripts/release-local install "<app path printed by build>"
scripts/release-local rollback            # back to the previous release and its data

# Detached install (an orchestrator ends its turn right after starting it): add
# --notify so the installer messages the session once the new daemon is healthy
# (or the install failed). If the new daemon never comes up, nothing can deliver the message: the installer retries once for ~60 s across all sessions, logs a warning and exits with its own result. An idle session is not woken by the restart.
scripts/release-local install "<app>" --yes --notify <orchestrator-session-id> --log <install log> > <install log> 2>&1 &
```

- **Build stamping.** Every build records its version, full commit, UTC build
  time and dirty flag. `tallpine --version` prints them, the daemon logs them at
  start and reports them as `build` in `/healthz` and `/readyz`, the macOS
  bundle carries `VOBuildCommit`, `VOBuildDate` and `VOBuildDirty` in
  `Info.plist`, and the About panel shows the commit. The values come from
  `frontend/scripts/build-info.mjs` (and `scripts/daemon-build.sh`); set
  `TALLPINE_BUILD_VERSION`, `TALLPINE_BUILD_COMMIT`, `TALLPINE_BUILD_DATE` or `TALLPINE_BUILD_DIRTY` to
  pin them.
- **Signing and privacy prompts.** macOS keys the Documents, Desktop, Music
  and similar grants to the app's code identity. An ad hoc signature is a
  per-build cdhash, so every install would be a new app and ask again. `build`
  therefore signs with a stable identity (hardened runtime, existing
  entitlements, every nested binary including `tallpine` and the native modules):
  `TALLPINE_SIGN_IDENTITY`, else `APPLE_SIGNING_IDENTITY` (for example
  `Developer ID Application: Your Company (TEAMID)`; either can live in the
  untracked `~/.tallpine/release.env`, which the script sources). The same file may set
  `TALLPINE_RELEASE_GITHUB_REPO=<owner>/<repo>`, the GitHub repo `tallpine start` downloads
  releases from (stamped into the binary with the Team ID parsed from the
  identity; without it a built `tallpine start` downloads nothing). With no signing identity `build` refuses; pass
  `--allow-unsigned` (local test builds only) to get an ad hoc build with a loud warning (grants will not persist;
  the forge post-package hook reads `TALLPINE_ALLOW_UNSIGNED=1` the same way, and also reads the Electron fuses back and, for
  signed builds, checks `codesign --verify --deep --strict` plus the hardened runtime). The designated requirement then reads
  `identifier "ee.justdev.tallpine"` plus `certificate leaf[subject.OU]`
  (check with `codesign -d -r- <app>`); `build` fails if it does not, and
  `install` refuses an app whose `codesign --verify --deep --strict` fails
  (`--force` overrides) and warns about an ad hoc one. The first signing run
  may show "codesign wants to use key ...": answer **Always Allow**, or every
  build will block on it. Grant Documents once after the first install with the
  stable identity; later installs keep it. Notarization is optional and
  needed for Jamf (a PPPC profile can pre-grant Documents access by team id).
  One-time setup: `xcrun notarytool store-credentials tallpine-notary
  --apple-id <id> --team-id <TEAMID>` (or the API key form with `--key`,
  `--key-id`, `--issuer`), then put `TALLPINE_NOTARY_PROFILE=tallpine-notary`
  in `~/.tallpine/release.env`.
  `scripts/release-local notary-check` verifies the profile without a build
  (prints ok or failed and the submission count only). Plain `build` is
  signed (hardened runtime, helper identities verified) but **not notarized**,
  which keeps daily builds fast and prints one line saying so. Use `build
  --notarize` for every build that goes to `publish` (website/updater): it
  needs the profile, exports it to forge, which notarizes and staples, and
  then fails unless `xcrun stapler validate` passes on the app (and dmg) and
  `spctl -a -vvv -t execute` reports `accepted, source=Notarized Developer ID`.
  `publish` refuses an app that was not built that way.
- **`request-build`** (inside a fenced agent shell it asks the daemon via `tallpine release request-build`, since agents cannot write `release-builds`) fetches origin/main: if its head already passed local CI
  and has no build yet (no `build-times.tsv` row), it builds it right away
  (detached, never installs, log `build-<sha8>.log` next to `local-ci.tsv`, one
  line to the orchestrator when done). Otherwise it drops a marker in
  `~/.tallpine/release-builds/` and the local CI loop builds the next main
  commit that passes. `build` holds a per-commit lock
  (`release-builds/.building-<sha>`, taken over after 45 minutes), so two
  builds of one commit never run at once. Install it yourself afterwards.
- **Build output**: `build` prints one line per step (deps, package, verify,
  notary-verify; package includes notarization with `--notarize`, since forge
  does it) and a summary, sends npm/electron output to `release-build.log` in
  the build tree, and appends one line per build to
  `~/.tallpine/releases/build-times.tsv` (last 200 kept; `install-*.log` keeps
  the newest 20). The `package` step is broken down as `package.<phase>=Ns`
  (npm scripts incl. the Go daemon build, prepackage, vite, forge-package,
  finalize-codesign, postpackage, notarize) in the summary, log and TSV.
- **`build [<ref>]`** fetches origin, checks out the exact commit in a clean
  worktree under `~/.tallpine/release-builds/<sha>`, runs `npm ci` and
  `npm run package` there, and prints the `.app` path and SHA. It refuses a
  commit whose latest row in the local CI results
  (`~/.tallpine/local-ci/results/local-ci.tsv`, override with `TALLPINE_RELEASE_CI_TSV`) is
  not `pass`, unless you pass `--force`. It keeps the newest three build trees.
- **`install <app>`** checks the bundle id, the build commit and its CI row,
  and refuses an app built from a dirty tree unless you pass `--force`. It
  refuses when another copy of the app (same bundle id, another path) is
  running. It then asks for confirmation (`--yes` skips the question), stages
  a copy, quits the app running from the install path (SIGTERM, the same
  clean quit as Cmd-Q: every agent stops and resumes on its next message, and
  unsent chat drafts are discarded), stops a remaining daemon with
  `/shutdown`, waits for it to exit, and checks that nothing still has the
  database open.
  With everything stopped it clones (`cp -c`, APFS) the data dir into
  `~/.tallpine/releases/<time>-<previous commit>/data`, skipping `worktrees`, `tmp`,
  `caches`, `logs`, `artifacts` and `import-staging`, and copies
  `~/.tallpine/app-state.json`. The previous app moves into the same directory, so
  `~/Applications` never holds two bundles with one bundle id. The new app
  moves into place, relaunches, and the script waits for `/healthz` and
  `/readyz` and checks the reported commit. Any failure or Ctrl-C before the
  new app is in place is undone: the snapshot is written as `.partial` and
  removed, the previous app moves back and is relaunched if it was running.
  Closing the terminal (SIGHUP) is handled the same way, and a second Ctrl-C
  is ignored until the undo finishes. The newest three rollback points are
  kept.
- **`rollback`** shows the point's age and asks for confirmation, refuses a
  point recorded for another data dir or app path, then quits the app and
  daemon, moves the current data and app aside into
  `~/.tallpine/releases/rolled-back/`, restores the latest rollback point's data
  and app, relaunches and waits for health. A failure or Ctrl-C midway puts
  the current data and app back. The replaced release
  stays in `rolled-back/` (newest three kept), so you can `install` it again.
  Data written after the upgrade is in that directory too, not deleted.
- **Testing the script.** `--state-dir`, `--data-dir` and `--app-dir` point it
  at other directories; with any of them it never reads the live
  `running.json`. `scripts/release-local.test.sh` runs install, rollback,
  status and retention against temp directories and a fake app.


### Jamf profile and schema

`scripts/release-local pppc <app> [--out <file>]` builds the Jamf PPPC and
login-items profile (Documents, Desktop and Downloads for the app and daemon,
plus a `com.apple.servicemanagement` Team ID rule) from the signed app's
`codesign` designated requirements. It refuses ad hoc, unsigned and Team-ID-less
builds, and the Team ID is never committed. `publish --publish-github` attaches
the profile as `Tallpine-PPPC.mobileconfig` and the generated Jamf Custom Schema
as `tallpine-jamf-schema.json`. It also builds `Tallpine-<version>.pkg`
(`release-local.d/pkg.sh`: `pkgbuild` component package installing to
`/Applications`, no scripts, `productsign` with `TALLPINE_INSTALLER_IDENTITY`,
notarized and stapled via `notarize_artifact`, verified with `pkgutil`); publish
fails before `gh release create` when the identity is unset or notarization
fails, and `build` never makes a pkg. After changing a lockable setting, regenerate
the schema with `UPDATE_JAMF_SCHEMA=1 go test ./internal/settingsreg` and list
the key in the table in `docs/agent-policy.md` (a test checks it). Tests:
`scripts/release-local-pppc.test.sh`, `scripts/release-local-publish.test.sh`
(fake pkgbuild/productsign/notarytool/pkgutil).
Admin guide: [`jamf.md`](jamf.md).

## Troubleshooting

### Backend build / test failures

| Symptom                              | Likely cause                               | Fix                                                                                                                                                                                                                                                                               |
| ------------------------------------ | ------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go: go.mod requires go >= 1.27`     | Wrong Go version                           | `go version`; install Go 1.27.3+ from [go.dev](https://go.dev/dl/)                                                                                                                                                                                                                                    |
| `sqlc generate` produces errors      | Query SQL syntax or schema migration issue | Check `backend/internal/storage/sqlite/queries/` for SQL syntax, placeholder counts, and referenced columns/tables; if you changed the schema, add a new migration in `backend/internal/storage/sqlite/migrations/` instead of editing an existing one, then rerun `npm run sqlc` |
| `openapi.yaml` is stale              | Changed DTOs without regenerating          | Run `npm run api` from repo root                                                                                                                                                                                                                                                  |
| `golangci-lint` failures             | Linter version mismatch                    | Install v2.15.0 or use `make lint` from root                                                                                                                                                                                                                                   |
| Tests fail with "connection refused" | Test tries real daemon                     | Tests should use `httptest`; check for `go test ./...` without a live daemon                                                                                                                                                                                                      |

### Frontend build / test failures

| Symptom                               | Likely cause            | Fix                                                          |
| ------------------------------------- | ----------------------- | ------------------------------------------------------------ |
| `npm run typecheck` has type errors   | API types out of sync   | Run `npm run api` from repo root to regenerate               |
| `npm run dev` fails on native modules | Missing build tools     | Install Python + C++ build tools for `node-gyp`              |
| `npm install` or `npm ci` fails       | Node.js version too old | `node --version`; must be 24+ (see prerequisites above) |
| Blank window or crash on Linux        | Broken GPU driver stack | Start with `TALLPINE_DISABLE_GPU=1` to skip hardware acceleration  |

### Code generation drift

If CI fails on `sqlc-drift`, run the pinned generator from the repository root
and commit the generated storage changes together with their SQL/config source:

```bash
npm run sqlc
git diff --exit-code -- backend/internal/storage/sqlite/gen
```

The diff check should pass after committing the regenerated files. Do not edit
`gen/` by hand or use a different sqlc version; the version in `package.json` is
authoritative. Review any unexpected query or generated type changes before
committing. The storage tests also check that the config is valid YAML.

If CI fails on the `api-drift` check, the OpenAPI-generated files are out of sync with source. Regenerate them locally and commit the updated files:

```bash
npm run api
```

If regeneration introduces unexpected diffs beyond your changes, check that your local tool versions match CI (Go 1.27.3+, Node 24, npm 10+).

## OpenAPI spec and generated types

The API is defined in Go controller DTOs and operation registrations. Edit
these source files, then regenerate:

```bash
npm run api
```

The generated artifacts are:

- `backend/internal/httpd/apispec/openapi.yaml`
- `packages/shared/api/schema.ts`

Both must be committed together with the Go changes. CI verifies they are
in sync.
