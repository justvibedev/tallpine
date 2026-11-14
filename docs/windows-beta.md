# Windows beta

Tallpine runs on Windows 11 x64 as a beta. The installer is unsigned and is
published as a GitHub prerelease on the releases-only repository
`justvibedev/tallpine-releases`. The macOS release feed is separate and does
not change.

## What differs from macOS

| Area | Windows beta |
| --- | --- |
| Architecture | x64 only. |
| Installer | Per-user NSIS installer (`Tallpine Setup <version>.exe`), unsigned. |
| Updates | No in-app updater. Settings > Updates shows the running version and a **Download the latest Windows installer** link to the releases page. Install the new build over the old one. |
| Terminal runtime | ConPTY (no tmux). |
| Security mode | Not available. The Protected choice and the project **On** option are disabled, and the daemon refuses to turn it on. |
| Claude sign-in | `~/.claude/.credentials.json` or `ANTHROPIC_API_KEY`; Windows Credential Manager is not read. |
| Project env secrets | Unavailable, with the existing error. |
| launchd features | None. The app starts and stops the daemon itself. |
| Not shipped | cloudflared (phone pairing over a tunnel may be unavailable), the closed-lid wake helper, Stay Awake. |

## Prerequisites

Install these yourself; the app's system check reports anything missing.

- [Git for Windows](https://git-scm.com/download/win) (Git Bash is the default shell).
- [Node.js](https://nodejs.org/) LTS.
- The agent CLIs you use: `claude` (Claude Code) and/or `codex`.

To build the installer you also need:

- [PowerShell 7](https://learn.microsoft.com/powershell/scripting/install/installing-powershell-on-windows) (`winget install Microsoft.PowerShell`). The scripts do not run on Windows PowerShell 5.1.
- [Go](https://go.dev/dl/) at the version in `backend/go.mod`.
- The [GitHub CLI](https://cli.github.com/) (`gh auth login`), only to publish.

Building on a Windows 11 ARM64 VM (x64 emulation, such as the owner's UTM VM) also needs these, all x64 builds:

- The x64 [WiX Toolset](https://wixtoolset.org/) (the NSIS/Squirrel maker shells out to it) and the x64 Visual C++ redistributable.
- [Python 3.12](https://www.python.org/downloads/) and Visual Studio 2022 Build Tools with the "Desktop development with C++" (VCTools) workload, for `node-gyp` native modules.
- Git checkouts with `core.autocrlf=true` are fine: `.gitattributes` pins the hash-verified pricing catalog to LF. Without that pin the daemon logs `validate bundled pricing catalog ... hash mismatch`.

Run the scripts from a PowerShell 7 prompt; Windows PowerShell 5.1 and the default execution policy refuse them. Long or GUI runs started over SSH die with the session, so start them as a scheduled task.

Known limitation: the Codex account store requires its private directories to be owned by your user. A session whose token is always elevated (UAC off, or an SSH admin session) creates `BUILTIN\Administrators`-owned directories, and the daemon logs `Codex account store initialization failed reasonCode=account_storage_unsafe`. Run Tallpine from a normal, non-elevated desktop session.

## Install

1. Download `Tallpine Setup <version>.exe` from the
   [releases page](https://github.com/justvibedev/tallpine-releases/releases).
2. Optional: check the download against the `.sha256` file next to it:
   `(Get-FileHash -Algorithm SHA256 '.\Tallpine Setup <version>.exe').Hash`
3. Run the installer. Windows SmartScreen shows **Windows protected your PC**
   because the installer is not signed. Choose **More info**, then
   **Run anyway**. If your browser blocks the download, keep it from the
   download list.
4. To update, download and run the newer installer; it replaces the old build
   and keeps your data in `%USERPROFILE%\.tallpine`.

## Build and publish (owner VM)

On the Windows 11 x64 VM, from a clean checkout of the commit to ship, with
Tallpine **not** installed (run the smoke before the manual E2E install, or
uninstall first):

```powershell
pwsh scripts/windows/build-beta.ps1 -Build 1
pwsh scripts/windows/smoke.ps1 -Installer "frontend\out\windows-beta\Tallpine Setup <version>-beta.1.exe"
pwsh scripts/windows/build-beta.ps1 -Build 1 -Publish -Installer "frontend\out\windows-beta\Tallpine Setup <version>-beta.1.exe"
```

- The version is `<frontend/package.json version>-beta.<Build>`. Use a new
  `-Build` number for every build you share; publishing refuses a tag that
  already exists.
- The script writes the installer and `<installer>.sha256` to
  `frontend\out\windows-beta` (`-OutDir` to change), restores
  `frontend/package.json` afterwards, and prints the installer path.
- It refuses a dirty checkout; `-AllowDirty` builds one anyway for local
  testing (the build is marked dirty).
- `-Publish -Installer <path>` publishes the installer the smoke tested,
  without building again. Without `-Installer`, `-Publish` builds and then
  publishes.
- `-Publish` creates prerelease `v<version>` on `-Repo`
  (default `justvibedev/tallpine-releases`) with the installer and checksum.
  The script never creates the repository: create it first with one commit
  (a README), since a release needs a commit to tag.

`smoke.ps1` installs silently into a temp directory, starts the bundled daemon
on a scratch data dir and port 3191, checks `/healthz`, opens a `cmd` shell
terminal through the API (ConPTY), checks it stays alive, closes it and
uninstalls. Its data stays in a temp directory; it does not touch
`%USERPROFILE%\.tallpine`. The smoke installer has the real app's id, so on a
machine where Tallpine is installed it would uninstall that copy (and close a
running one). The script therefore exits with code 2 if it finds an installed
or running Tallpine; `-ReplaceExistingInstall` overrides that, and leaves no
Tallpine installed afterwards. `-Keep` leaves the install and data for
inspection.

The same build and smoke run in the manual **Windows beta smoke** workflow
(`.github/workflows/windows-beta-smoke.yml`, Actions > Run workflow) on
`windows-latest`. It never runs on push and never publishes; the installer it
built is attached to the run for seven days.

## Manual E2E checklist (Windows 11 x64)

Run on a fresh Windows 11 x64 user with the prerequisites installed. Record
the version, commit and result of each step; file each failure as a
Windows-scoped fix.

- [ ] Installer downloads; SmartScreen **More info > Run anyway** works; install completes.
- [ ] Start menu and desktop shortcuts open Tallpine.
- [ ] First run: onboarding completes; the system check lists Git, Node, `claude` and `codex` correctly (and names a missing one).
- [ ] Settings > Updates shows the installed version and the download link opens the releases page in the browser.
- [ ] Settings > Security: Protected is disabled with "Not available on Windows"; Standard and Unrestricted can be picked. A project's Security mode **On** option is disabled.
- [ ] Add a local git repository as a project.
- [ ] Open a shell terminal tab (Git Bash, then PowerShell); typing, resize and scrollback work.
- [ ] Start an orchestrator session; it answers in the terminal and in Chat.
- [ ] Spawn a Claude Code worker; it creates a worktree, edits a file and finishes.
- [ ] Spawn a Codex worker; same as above.
- [ ] Kill a session; its worktree is removed.
- [ ] Quit Tallpine; no `tallpine.exe` daemon is left running (Task Manager). Relaunch; sessions and projects are still there.
- [ ] Install the next beta over this one; data is kept and the version in Settings changes.
- [ ] Uninstall from Windows Settings > Apps; the app is removed and `%USERPROFILE%\.tallpine` is left for the user to delete.
