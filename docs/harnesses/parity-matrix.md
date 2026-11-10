# Harness parity matrix

What each shipped harness (`claude-code`, `codex`, `kiro`, `deepseek-harness`) supports
today, and the limits that remain. Verified against the code at `main` after
the B221 parity work (Codex hooks/compaction/`/clear`, Kiro engine/hooks/ACP chat,
DeepSeek hooks and reviewer). Paths are repo-relative from `backend/internal/` (harness plugins are under `adapters/agent/<harness>/`, chat drivers under `adapters/chatdriver/`).

Legend: yes · partial · no. "Limit" rows are the known gaps; the public docs
(`frontend/src/docs/content/plugins/agents/`) repeat them per harness. These
limits are stated as facts, not labelled experimental; revisit after the live
Kiro and DeepSeek runs.

## Chat

| Feature | Claude | Codex | Kiro | DeepSeek | Evidence |
|---|---|---|---|---|---|
| Streaming, tools, approvals, interrupt, resume | yes | yes | yes | yes | `adapters/chatdriver/{claudeacp,codexappserver,kiroacp,deepseekharnessacp}/driver.go`, `adapters/chatdriver/registry/registry.go:55-75` |
| Steer mid-turn | partial (bridge-advertised) | yes | no | no | `adapters/chatdriver/codexappserver/driver.go:158`, `adapters/chatdriver/acp/driver_capabilities.go` |
| Manual and idle compaction | yes | yes | no | no | `adapters/chatdriver/codexappserver/driver.go:139`; no `Compaction` in `adapters/chatdriver/kiroacp/driver.go:44-52` or `adapters/chatdriver/deepseekharnessacp/driver.go:42-49` |
| Usage / cost | yes | yes (+rate limits) | credits per turn | yes | `adapters/chatdriver/codexappserver/driver.go:126,135`, `adapters/chatdriver/deepseekharnessacp/usage_tracker.go` |
| Model picker | partial | yes | yes | no | `adapters/chatdriver/kiroacp/driver.go:51`, DeepSeek caps list has no `Models` |
| Plan mode, diffs, rollback/fork/rename/skills | partial | yes | no | no | `adapters/chatdriver/codexappserver/driver.go:127-150` |
| Context breakdown by category | yes | no | no | no | `adapters/chatdriver/acp/context_breakdown.go` (Claude extension) |

## Terminal

| Feature | Claude | Codex | Kiro | DeepSeek | Evidence |
|---|---|---|---|---|---|
| Hook events registered | 12 | 12 | 6 | 7 | `adapters/agent/claudecode/hooks.go:48`, `adapters/agent/codex/hooks.go:74-90`, `adapters/agent/kiro/hooks.go:84-91`, `adapters/agent/deepseekharness/hooks.go:189-197` |
| Permission-request event | yes | yes | no (screen detection) | no | `adapters/agent/kiro/hooks.go:80-83`; no `PermissionRequest` in the DeepSeek list |
| Error state (failed turn) | hook (`StopFailure`) | rollout poll | session-log poll | no | `adapters/agent/claudecode/hooks.go:58`, `adapters/agent/codex/turn_failures.go:44`, `adapters/agent/kiro/turn_failures.go:31`, `daemon/lifecycle_wiring.go:84` |
| Approvals answered from UI/API | yes | yes | yes | no | `adapters/agent/kiro/terminal_approval.go`; no counterpart in `deepseekharness/` |
| Terminal usage / context % | yes | yes | credits + context % | no | `adapters/agent/kiro/terminal_usage.go:25-94`; no usage reader in `deepseekharness/` |
| Terminal compaction | native `/compact` | native `/compact` + Pre/PostCompact hooks | `/compact` sent raw by Tallpine (kiro-cli 3.0.0) | no | `session_manager/send.go:308-323`, `adapters/agent/codex/hooks.go:88-89` |
| Subagent tracking | yes | yes | no | yes | `adapters/agent/activitydispatch/facts.go` |
| Native continuation caps | yes | yes | session id from hook | restore only (one-shot task) | `adapters/agent/kiro/continuation.go:23`, `adapters/agent/deepseekharness/deepseekharness.go:10-24` |
| Native reviewer | yes | yes | yes | yes | `domain/reviewerharness.go:13-18`, `adapters/reviewer/` |
| Accounts / switch / failover | yes | yes | no | no | `adapters/agentaccount/` has only `claude.go`, `codex.go` |
| Security-mode sandbox | yes | capped sandbox | permission presets | permission presets | `agentpolicy/rules/permissioncap` |

## Remaining limits (what the public docs state)

- **Codex:** no `Notification` hook, so a terminal error state comes from the rollout poll rather than an immediate hook. No context breakdown by category.
- **Kiro:** no permission-request hook (approval dialogs are detected from the screen); no steer or compaction in Chat; no account switching; no subagent tracking.
- **DeepSeek:** terminal approvals are not answerable (no permission event); no terminal usage or context readout; terminal is a one-shot task; Chat has no model picker, compaction or steer; no account switching.
