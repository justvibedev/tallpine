import type { PlanView } from "../lib/plan-api";

export function planFixture(over: Partial<PlanView> = {}): PlanView {
	return {
		id: "plan_1", itemId: "bl_1", status: "awaiting_answers", startedAt: "2026-11-01T00:00:00Z", updatedAt: "2026-11-01T00:00:00Z",
		questions: [
			{ id: "q1", text: "Which store?", reason: "Both work", recommended: "b", options: [{ id: "a", label: "Keep SQLite" }, { id: "b", label: "Add Postgres" }] },
			{ id: "q2", text: "Backfill?", recommended: "no", options: [{ id: "yes", label: "Yes" }, { id: "no", label: "No" }] },
		],
		assumptions: [{ id: "a1", text: "No new dependency" }, { id: "a2", text: "English only" }],
		prSplit: [], reviewFocus: [],
		...over,
	};
}
