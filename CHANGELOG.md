# Changelog

All notable user-visible changes to Tallpine. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Entries reference merged PRs (`#N`).

Tallpine is a hard fork of Agent Orchestrator (Apache-2.0); history before the fork is upstream's and is not listed here.

## [0.13.9]

### Fixed
- The closed Sessions panel no longer paints under the rail (#1083).
- Install output no longer shows harmless pkgbuild write noise (#1085).
- Auto-close reports the right cause after `pr land --kill-worker` (#1086).
- A failed install names the process that relaunched and changed `ao.db` during the snapshot (#1087).
- Agents left behind when the chat host is killed with SIGKILL are reaped (#1088).

## [0.13.8]

### Changed
- Restart and resume messages name the background jobs a restart killed (#1076).
- Dev mode audit lines state their real scope instead of always saying cross-project (#1073).
- The reviewer sees the backlog item's current done-when (#1077).

### Fixed
- `pr land` dry-run matches the real land for a rebased head (#1074).
- Merging a PR closes a backlog item that has a done-when only if the PR title claims it (#1078).
- Cleanup no longer repeats a warning on every sweep of archived-project sessions (#1075).
- `release-local` notifies `--notify` sessions when the stale-checkout guard refuses (#1079).
- The workspace watcher keeps draining FSEvents while its filter runs git (#1070). This is one part of an ongoing investigation into macOS UI lag and fseventsd growth, which is not resolved.

### Internal
- Test, CI, and internal-doc changes only (#1069, #1071, #1072, #1080, #1081).

## [0.13.7]

### Added
- Pick a project's GitHub account from your signed-in accounts, with clearer repository settings (#1067).
- A busy reviewer queues a rerun instead of dropping it, and resumes after a restart (#1065).

### Changed
- Cleanup tells you when it keeps a worktree (#1063).
- Spawning a backlog item with an unfinished blocker is refused unless you pass `--force` (#1056).

### Fixed
- Daemon git fetch and land run as the project's GitHub account (#1066).
- Reviewer and local CI accounts are chosen per repository, and failures are surfaced (#1050).
- Squash commits keep the PR-body trailers (#1054).
- A project can use a canonical repo URL on a different host, and tracks it (#1060).
- Orchestrators no longer change git remotes or publish to new hosts (#1061).
- Cleanup stops retrying teardown of sessions in archived projects (#1064).
- Workers get branch names Git can create (#1042).
- Plans hold auto-approve for owner-flagged decisions and retire abandoned child tasks on replan (#1041).
- Finished workers with no merged PR are closed (#1039).
- Auto-review follows the project setting for live workers, and test-only paths no longer escalate (#1043).
- Owner messages sent with "last" always resolve to the newest owner message (#1049).
- Sending to a session that needs re-authentication reports unconfirmed delivery (#1048).
- A mid-turn orchestrator is no longer reported as a failed delivery (#1047).
- The rebase warning is skipped when the PR head already contains the merge commit (#1046).
- A worker's undelivered turn result is carried in the close notice (#1045).
- Stale tool-server status clears when a project conversation is rebound (#1044).
- `review ls` shows the branch for no-PR reviews, and land notes local-ahead commits on non-hosted repos (#1059).
- The CLI falls back to loopback TCP when the daemon socket is unreachable (#1057).

### Internal
- Test and CI only: regenerated sqlc models, stabilized flaky tests, and a desktop-dev skill update (#1040, #1051, #1052, #1053, #1055, #1058, #1062).

## [0.13.6]

### Changed
- Updater errors are written to `~/.tallpine/logs/updater.log` (#1037).

### Fixed
- The in-app updater no longer fails with "Cannot read properties of null (reading request)" on every update check (#1037). This affected 0.13.4 and 0.13.5. Installs on those versions must update to 0.13.6 manually once; after that, automatic updates work.

## [0.13.5]

### Added
- Kiro chats detect a failed turn from the session log and report it instead of showing it as complete (#1031).
- A Kiro terminal session shows context usage as a percentage, read from the session directory next to credits (#1033).
- `/compact` is delivered to a Kiro terminal as a raw slash command (#1035).

### Changed
- The sidebar shows a yellow icon for an unanswered orchestrator Needs-you reply (#1025).
- Kiro chats send no effort setting, because kiro-cli 3.0.0 advertises no effort option (#1021).

### Fixed
- Kiro chats can select auto and bypass modes again (#1023).
- A started Kiro chat switches to the terminal UI (#1027).
- The Kiro context meter shows a percentage, not a token count (#1029).
- Manage agents in the agent picker opens Settings > Harnesses (#1022).
- Scrolling the mouse wheel over a long harness model list scrolls that list instead of the page (#1024).
- The desktop app explains a daemon that waits for or is refused the data-dir lock (#1020).
- The local release installer no longer aborts when the app relaunches during the database snapshot, and its undo no longer waits minutes when the daemon is already back (#1034).

## [0.13.4]

### Fixed
- Orchestrators no longer deny a worker's own deletes and writes in its $TMPDIR as outside the workspace; the path is checked and approved (#1013).
- The release .dmg is signed and notarized when published, so Gatekeeper accepts it (#1014).
- The queued-messages dock starts collapsed, so it no longer covers chat text when several messages are queued (#1015).
- The daemon force-exits when shutdown hangs, and a replacement daemon reclaims the data-dir lock from a hung predecessor instead of refusing to start (#1016).
- When the daemon cannot start, for example because the previous daemon still holds the data dir, the desktop app shows what happened, which process it is waiting for, the daemon's error and a Retry button instead of an empty window, and starts again on its own once that process exits (#1020).

## [0.13.3]

### Fixed
- The daemon no longer freezes the first time a session starts a background task (all saves stopped, chats stuck running, Stop and paste dead, restart to an empty window). Regression from 0.13.2 (#1007); fixed in #1011. Upgrading from 0.13.2 is recommended.

## [0.13.2]

### Fixed
- No false "ended on its own" notice when a session's close was caused by Tallpine itself (#1003).
- Right-clicking a path in the chat shows the full context menu again, not only the floating Add to chat action (#1004).
- Orchestrator chats no longer auto-open the Browser from links in their own replies (#1005).
- Running background subagents are shown in the chat (#1007).

### Changed
- Local image paths in chat replies render as inline thumbnails, and a plain click on any other local file path opens it (#1006).

### Internal
- Test-only: reap stale chat-host orphans from hard-killed e2e runs and regenerate a wire-tag golden (#1008, #1009).

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
