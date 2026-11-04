import type { PlanView } from "../../lib/plan-api";
import type { PlanDraft } from "./planDockModel";

// Unsent answers survive collapsing a card, re-rendering and an app reload.
const key = (planId: string) => `tallpine.planDraft.${planId}`;

export function loadPlanDraft(plan: PlanView): PlanDraft | null {
	try {
		const raw = localStorage.getItem(key(plan.id));
		const parsed = raw ? (JSON.parse(raw) as PlanDraft) : null;
		return parsed && typeof parsed.choices === "object" && typeof parsed.corrections === "object" ? parsed : null;
	} catch {
		return null;
	}
}

export function savePlanDraft(planId: string, draft: PlanDraft): void {
	try { localStorage.setItem(key(planId), JSON.stringify(draft)); } catch { /* storage unavailable: the draft stays in memory */ }
}

export function clearPlanDraft(planId: string): void {
	try { localStorage.removeItem(key(planId)); } catch { /* ignore */ }
}
