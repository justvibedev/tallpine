import { useAgentPolicy } from "./useAgentPolicy";

/** The managed planner locks (B525): only the managed file or environment can set them. */
export interface PlannerLocks {
	/** The locked planning mode, or "" when nothing locks it. */
	mode: string;
	/** Plan auto-approval is locked off in every project. */
	autoApproveOff: boolean;
}

/** enabled is false for a remote host's project: the lock is this machine's policy and does not apply there. */
export function usePlannerLocks(enabled = true): PlannerLocks {
	const { policy } = useAgentPolicy(undefined, enabled);
	const fields = policy?.rules?.find((rule) => rule.name === "planner")?.fields ?? [];
	const mode = fields.find((f) => f.name === "mode");
	const autoApprove = fields.find((f) => f.name === "autoApprove");
	return {
		mode: mode?.locked ? String(mode.value ?? "") : "",
		autoApproveOff: autoApprove?.locked === true && String(autoApprove.value ?? "") === "off",
	};
}
