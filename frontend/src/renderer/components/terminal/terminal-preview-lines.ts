/** Worker and reviewer preview lines shown in the terminal placeholder. */
import { type WorkspaceSession } from "../../types/workspace";

export function workerPreviewLines(session: WorkspaceSession | undefined, provider: string): string[] {
	if (session?.id === "tallpine-demo-orchestrator") {
		return [
			"> Go through my Linear backlog and let's plan which tasks to spawn off",
			"",
			"Ran 3 shell commands",
			"",
			"Here's the backlog triage. Half of it is already in flight — don't spawn those.",
			"",
			"Already covered — don't spawn (session → PR):",
			"— terminal polish → PR #318, changes requested",
			"— browser preview stack → PRs #319/#320, in review",
			"— README screenshot assets → PR #323, approved and mergeable",
			"",
			"Plan: 3 sessions worth spawning",
			"",
			"┌───┬────────────────────┬──────────────────────────────────────────┬──────────────────────────────────┐",
			"│ # │ Session            │ Scope                                    │ Why now                          │",
			"├───┼────────────────────┼──────────────────────────────────────────┼──────────────────────────────────┤",
			"│ 1 │ new-task-flake     │ NewTaskDialog smoke test flakes on Enter │ Failing PR #324's e2e; small fix │",
			"│ 2 │ checkout-retries   │ e2e retries leak state between runs      │ Flakes 1-in-5 on CI; well scoped │",
			"│ 3 │ session-pr-surface │ PR checks missing on board cards         │ Additive; touches board only     │",
			"└───┴────────────────────┴──────────────────────────────────────────┴──────────────────────────────────┘",
			"",
			"Want me to spawn all three? I'd put #1–2 on codex and #3 on claude-code.",
			"",
			"> yes, spawn all three",
			"",
			"Running 3 shell commands…",
			'└ $ tallpine spawn --project tallpine-demo --name "new-task-flake" --agent codex --prompt',
			'  "Fix the flaky NewTaskDialog smoke test: submit is debounced 300ms while the',
			'  e2e check asserts synchronously. Reproduce, fix, and push to update PR #324."',
			"PASS 3 sessions spawned — board updated",
		];
	}
	if (session?.id === "demo-review-stack") {
		return [
			'$ rg "previewUrl|Browser" frontend/src/renderer',
			"frontend/src/renderer/components/SessionInspector.tsx: Browser tab selected after tallpine preview",
			"frontend/src/renderer/hooks/useBrowserView.ts: preview revision re-navigates the view",
			"$ tallpine preview http://localhost:5173",
			"DONE preview target set for demo-review-stack",
			"$ npm --prefix frontend run typecheck",
			"PASS TypeScript project references are clean",
			"TODO wait for reviewer on PR #320 before merging the stack",
		];
	}
	if (session?.id === "demo-working") {
		return [
			`$ ${provider} --continue`,
			"Reading renderer board and inspector components...",
			"Updated demo workspace data for README screenshots",
			"$ npm --prefix frontend test -- SessionsBoard SessionInspector",
			"PASS 18 tests passed",
			"DONE board has Working, Needs you, In review, and Ready to merge populated",
		];
	}
	if (session?.id === "demo-needs-input") {
		return [
			"$ git diff --stat",
			"frontend/src/renderer/components/TerminalPane.tsx | 41 +++++++++++++++++",
			"frontend/src/renderer/styles.css                 | 27 +++++++++++",
			"WARN reviewer requested a tighter terminal activity sample",
			"TODO confirm whether to keep the toolbar density change",
		];
	}
	if (session?.id === "demo-ci-failed") {
		return [
			"╭────────────────────────────────────────────╮",
			"│ >_ OpenAI Codex (v0.133.0)                 │",
			"│ model:        gpt-5.5 high  /model to change",
			"│ directory:    ~/tallpine-demo/demo-new-task-flake",
			"│ permissions:  YOLO mode                    │",
			"╰────────────────────────────────────────────╯",
			"",
			"• I'll start from the failing check output, reproduce the Enter-submit",
			"  path locally, then patch and push.",
			"",
			"• Ran npm test -- NewTaskDialog",
			"└ PASS 12 tests passed",
			"",
			"▲ tallpine send · CI failed on PR #324. The failing checks are e2e (NewTaskDialog",
			"  submits with Enter). Investigate and push a fix.",
			"",
			'• Ran rg -n "onKeyDown|Enter" src/components/NewTaskDialog.tsx',
			'└ src/components/NewTaskDialog.tsx:88:  onKeyDown={(e) => e.key === "Enter" && scheduleSubmit()}',
			"  src/components/NewTaskDialog.tsx:141: const scheduleSubmit = debounce(submit, 300)",
			"  … +42 lines (ctrl + t to view transcript)",
			"",
			"Found it: submit is debounced 300ms, the check asserts immediately.",
			"Patching the handler and re-running e2e…",
		];
	}
	return [
		`$ ${provider} --status`,
		"Reading task context and local diff...",
		"Running focused validation for the current session",
		"PASS demo terminal is populated for screenshots",
	];
}

export function reviewerPreviewLines(session: WorkspaceSession | undefined): string[] {
	return [
		"$ tallpine review submit --session " + (session?.id ?? "demo-session"),
		"Reviewing PR #319: browser preview rail renders inside Tallpine",
		"PASS implementation matches the requested README screenshot flow",
		"Reviewing PR #320: stacked PR review rows",
		"WARN keep multiple review rows visible before taking the screenshot",
		"DONE submitted batched review results",
	];
}

// Agents whose full-screen TUI keeps its own transcript and scrolls it only by
// keyboard, ignoring SGR wheel reports, get the wheel routed to PageUp/PageDown
// (see XtermTerminal's paneScrollsByKeyboard). None of the supported agents
// (Claude Code, Codex, Kiro) does: they use the normal terminal buffer and keep
// the SGR -> tmux scroll path.
