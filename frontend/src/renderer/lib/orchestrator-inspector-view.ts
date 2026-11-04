import type { InspectorView } from "../stores/ui-store";

/**
 * An orchestrator's panel holds Browser plus the Plan tab. Browser shows only when
 * explicitly chosen or targeted; with no choice the panel opens on Plan, and falls
 * back to Browser only when it is the sole tab (no plan yet).
 */
export function orchestratorInspectorView(stored: InspectorView | undefined, hasPlan: boolean): InspectorView {
	if (stored === "browser") return "browser";
	return hasPlan ? "plan" : "browser";
}
