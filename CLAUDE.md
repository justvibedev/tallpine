# CLAUDE.md

Read and follow [`AGENTS.md`](AGENTS.md) for repository layout, commands, coding conventions, and hard rules.

## App state lives under `~/.tallpine` only

All app state, the daemon's data dir, `running.json`, worktrees, and the Electron
supervisor's `userData` (Chromium cache, cookies, local/session storage, crash
dumps), must resolve under `~/.tallpine` (overridable via `TALLPINE_DATA_DIR`/`TALLPINE_RUN_FILE`).
Never write AO state to `~/Library/Application Support` or any other OS-default
app-data location. The sole read exception is an explicit user-initiated import
from a validated Chrome, Firefox, or Safari profile: read known source files
without modifying them and keep snapshots and results under `~/.tallpine`.
`frontend/src/main.ts` pins Electron's `userData` to
`~/.tallpine/electron`; do not remove that override. See the hard rule in `AGENTS.md`.

## Tallpine runs beside official AO

This repo is a hard fork that must coexist with an official Agent Orchestrator
install. Product `Tallpine`, CLI `tallpine`, app id
`ee.justdev.tallpine`, state root `~/.tallpine`, env prefix `TALLPINE_*`, daemon
port `3101`, `tallpine://` links, tmux socket `tallpine`, launchd labels
`ee.justdev.tallpine.*`. Read identity values from `backend/internal/brand/brand.go`
and `frontend/src/shared/brand.ts`; never reintroduce `ao`, `~/.ao`, `AO_*`, or
port 3001, and never touch the official install's `ao hooks` entries. See the
side-by-side hard rule in `AGENTS.md`.

## Design system

Before any visual or UI decision, read [`DESIGN.md`](DESIGN.md) (it also covers building UI and demoing it with `tallpine preview`).
