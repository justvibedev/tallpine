# Quick Reference

Natural-language-to-command mappings for common Tallpine tasks.

| You want to... | Command |
|---|---|
| Show me this webpage / open this page | `tallpine preview "<url>"` |
| Start an existing configured dev app | `tallpine preview start [configuration]` |
| Check or stop the worker's managed dev app | `tallpine preview status` / `tallpine preview stop` |
| Show this Markdown or HTML file without a server | `tallpine preview "<workspace-path>"` |
| Hand off a newly created browser-displayable artifact | `tallpine preview "<workspace-path>"` immediately after writing the primary artifact |
| Inspect and verify this webpage as the agent | `tallpine browser open "<url>"`, then `tallpine browser snapshot` |
| Click or fill a page element | `tallpine browser snapshot`, then `tallpine browser click <ref>` or `tallpine browser fill <ref> "<text>"` |
| Check frontend runtime failures | `tallpine browser errors` and `tallpine browser console` |
| Diagnose a request/API/CORS/auth/redirect failure when normal page evidence is insufficient | `tallpine browser network start`, reproduce once, then `tallpine browser network stop` |
| Check network capture without enabling it | `tallpine browser network status` or `tallpine browser network list` |
| Open the user's real Chromium debugging surface | `tallpine browser devtools open` |
| Close the shared DevTools window when explicitly requested | `tallpine browser devtools close` |
| Capture the page | `tallpine browser screenshot [path]` |
| Spawn a worker on issue N | `tallpine spawn --project <p> --issue N --name "<=100 chars>" --prompt "..."` |
| Message a running agent | `tallpine send --session <id> --message "..."` |
| Kill a session | `tallpine session kill <id>` |
| List sessions | `tallpine session ls` |
| Register a repo as a project | `tallpine project add --path <abs-path> --name <name>` |
| List projects | `tallpine project ls` |
| Check installed agent harnesses and readiness | `tallpine agent ls` (`--refresh` is orchestrator-only; workers get the cached catalog) |
| Save a repetitive agent task or exact command as a Cue | `tallpine cue list --json`, then `tallpine cue create --name "..." --prompt "..."` or `--command "..."` |
| Rename a worker session (not the orchestrator) | `tallpine session rename <id> "<name>"` |
| Restore a killed session | `tallpine session restore <id>` |
| Clean up terminated sessions | `tallpine session cleanup` |
| Make a Docker container this session starts survive Tallpine cleanup | `docker run --label tallpine.session=$TALLPINE_SESSION_ID --label tallpine.spare=true ...` |
| See a session's details | `tallpine session get <id>` |
| Open the desktop app | `tallpine start` |
| Check the daemon is up | `tallpine status` |
| Run health checks | `tallpine doctor` |
| Clear the preview panel | `tallpine preview clear` |
| List orchestrator sessions | `tallpine orchestrator ls` |
| Claim an existing PR for the current session | `tallpine session claim-pr <pr-ref>` (`TALLPINE_SESSION_ID`) |
| Claim an existing PR for another session | `tallpine session claim-pr <id> <pr-ref>` |
| Merge a pull request (pinned to the latest Tallpine-approved head, or `--expected-sha`) | `tallpine pr merge <pr-number> [--url <pr-url>] [--expected-sha <sha>]` |
| Resolve pull-request review threads | `tallpine pr resolve-comments <pr-number> [comment-id...]` |
| Submit a code review verdict | `tallpine review submit <session-id> --run <run-id> --verdict approved` |
| Get an independent / adversarial review of a Tallpine worker's implementation PR | `tallpine review trigger` (inside the owning worker), or `tallpine review trigger <worker-session-id>` |
| Review someone else's PR | Spawn or use a normal review-only worker without claiming the target PR; submit the requested provider review directly |
| Inspect, cancel, or rerun a review | `tallpine review ls [session-id]`, `tallpine review cancel [session-id]`, or `tallpine review trigger [session-id] --rerun` |
| Configure a project's default branch or model | `tallpine project set-config <id> --default-branch <branch> --model <model>` |
