# Changelog

All notable user-visible changes to Tallpine. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Entries reference merged PRs (`#N`).

Tallpine is a hard fork of Agent Orchestrator (Apache-2.0); history before the fork is upstream's and is not listed here.

No tagged Tallpine release exists yet; everything below is on `main` and ships with the first one.

## [Unreleased]

### Added
- Settings shows which GitHub login a project's token really is and warns when it is another account or cannot see the repo (#889).
- Security mode switch (off by default): Settings > Agent policy toggle, per-project inherit/on/off, and a "Not fenced" line in the session inspector for harnesses that cannot be fenced (#886).
- First-run notice that agents act with your permissions (#408).
- `tallpine browser viewport` command (#403).
- "Restart when idle" for a staged update (#400).
- Mobile: new, end and hand off lanes; lane switcher; every orchestrator lane kept per project (#394, #398, #399).
- Click copies links and paths in chat, Cmd+click opens them (#393).
- Salvage and remove dirty worktrees of ended sessions (#392).
- Auto-update from signed GitHub Releases (#385); local releases are notarized and stapled (#383).
- Product renamed to Tallpine: CLI `tallpine` (`tp`), state in `~/.tallpine`, `TALLPINE_*` environment variables, port 3101, `tallpine://` links, runs side by side with the official app (#356, #363, #365, #368). `tallpine migrate-from-vo` migrates earlier data.
- Orchestrator lanes with per-lane report routing, lane picker, End lane, and a cross-lane merge digest (`tallpine merges`) (#329, #330, #331, #333, #361).
- Account pool with placement policy and auto-failover on usage limits, off by default (#282, #291, #296, #298).
- Opt-in orchestrator answers to worker permission prompts; permission-request reports carry the exact command (#302, #304).
- Inbox replaces the bell popover; labelled navigation rail; Backlog lives in the rail; panel switcher in the top bar (#292, #295, #300, #283, #225).
- Backlog run mode and hard delete/purge for closed items (#184, #185, #199).
- Per-project GitHub account selection (#198, #286) and default-branch sanity notice (#287).
- Skills: Settings > Skills, choose which skills to bridge, commit created agent files (#246, #271, #276).
- Chat: context details popover with auto-compaction percent, compaction lifecycle, report cards for automation messages, stop a background task, batched pending messages (#249, #250, #320, #324, #260, #264).
- Per-model token usage per session in API, CLI and popover (#288).
- `tallpine orchestrator handoff` to a fresh orchestrator; `tallpine review wait` (#255, #305).
- Bundled tmux 3.8 (#259).

### Changed
- Upgrade note: a `GH_TOKEN` or `GITHUB_TOKEN` in the daemon environment no longer pins every project to one GitHub account; only `TALLPINE_GITHUB_TOKEN` does. Install and relaunch also strip these variables (#889).
- Projects follow GitHub repository renames; the canonical repo URL is editable (#396).
- `review trigger` refreshes the PR head and refuses a stale `--head` (#397).

### Fixed
- A project's GitHub account no longer gets another account's token when the daemon environment holds `GH_TOKEN` (#889).
- Orphan processes are reaped when a session ends; Go build cache is capped (#391).
- Mobile builds launch on iOS 27 (Expo SDK 58) (#389).
- Migration state stays under `~/.tallpine` (#395).
- Orchestrator-to-orchestrator sends and steers are framed as untrusted (#401).
- Tallpine branding in dialogs, tray, macOS display name, Privacy & Security and mobile pairing (#369, #376, #379, #380).
- `node_modules` symlinks no longer make a worktree dirty (#378).
- Claude family aliases (`sonnet`, `haiku`) accepted at spawn (#371).
- Stale worker reports dropped at delivery (#370).
- Workers interrupted by a daemon restart are resumed; idle workers woken on `changes_requested` (#237, #234).
- Session status dot is red only for failure or blocked (#236).
- Reliable auto-close of backlog items for merged PRs (#228).

### Security
- Bundled helper code signatures are verified before spawn (#406).
- Enterprise agent policy (off by default): permission cap, per-harness environment allowlist, GitHub credentials without env vars, managed policy via MDM, fail-closed on invalid policy (#214, #238, #242, #243, #244, #327, #357).
- Daemon token rotated on every start; data root permissions locked down; Unix sockets owner-checked (#208, #209, #217, #351).
- Remote API routes allowlisted; plain-LAN HTTP refused for agent-driving routes; per-device mobile tokens (#235, #347, #360).
- Electron hardening: fuses, IPC sender checks, CSP, deny-by-default permissions, signed-build checks (#215, #220, #353).
- Reviewers never load repository agent config and never APPROVE on GitHub (#354, #358).
- Worker text reaching orchestrators is framed as untrusted data (#318).
- Agent browsing goes through a resolver proxy with isolated tabs (#223, #227).
