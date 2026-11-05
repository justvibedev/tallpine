# Changelog

All notable user-visible changes to Tallpine. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Entries reference merged PRs (`#N`).

Tallpine is a hard fork of Agent Orchestrator (Apache-2.0); history before the fork is upstream's and is not listed here.

## [0.13.1]

### Added
- Pin projects to the top of the sidebar (#985).
- Settings > Reviews holds app-wide defaults that projects inherit: automatic review, planning mode and plan auto-approve; a project can still override each (#958, #959).
- `tallpine project set-config --plan-auto-approve-inherit` clears a project's own plan auto-approve value so it follows the app default (#959).
- Claude Code: per-role plugins and auto-memory settings for orchestrator, worker and reviewer sessions; models the installed Claude Code is too old for are gated (#994, #982); Chat sessions get the same per-role settings, including after a provider switch, and rarely used skills are listed by name only (#1001).
- Update the Codex and Claude Code casks while sessions are running (#984).
- An open local artifact refreshes live when its file changes (#988).
- Mobile: browse, comment on and restore a session's artifacts; paste images into the chat and reviewer composers (#987, #986).
- `tallpine send --relay-owner-message last` passes an owner message between sessions, verified by the daemon (#972).
- `tallpine session land` squashes an approved branch onto its target; a native review also works on a session branch that has no PR (#965, #961).
- Adding a project no longer requires a Git remote (#963).

### Changed
- Auto planning mode plans only work that splits into several PRs; single-PR work goes straight to a worker (#966).
- Local-only projects get prompts that name the local base and never push to a local-folder remote (#960).
- Local builds never self-update (#978).
- Bypass permissions is marked as a risk in the chat composer, and only the selected composer submenu closes (#992).

### Fixed
- The desktop app respawns the daemon after an unexpected exit (#977).
- Stay Awake responds faster and recovers after charging (#993).
- Session cleanup is idempotent, with a reconcile pass and session delete (#983).
- Killing a session waits behind automatic recovery and names the holder (#967); salvaged worktrees are removed (#962).
- Claude Code: tasks start on a renewable expired login and the banner clears once login works; a refused login is shown instead of a silent wait; the "Teach auto mode" dialog is dismissed before launch (#981, #954, #950).
- No "ended on its own" notice for sessions already killed, and no stale app-restart notice for sessions already working again (#998, #997).
- Long conversations load and render within bounds (#980).
- A failed manual review shows its stored reason (#979).
- `tallpine project set-config --config-json` no longer drops fields the CLI does not know (#996).
- A fresh project inherits the daemon default agent (#968).
- Mobile: the closed iOS drawer no longer eats header taps and the app launches on Projects; bubble images open in a viewer that does not freeze iOS taps; the orchestrator composer placeholder names the orchestrator (#973, #991, #990).
- Chat agents load `html_render` and show requested screenshots in chat (#1002).
- Stuck reports no longer press Ctrl-C in terminal orchestrators (#976); chat screenshot requests are routed to render (#975); the orchestrator does owner git chores (#957).

## [0.13.0]

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
