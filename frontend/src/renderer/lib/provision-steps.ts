import type { SessionProvisionStep } from "../types/workspace";

type WireProvisionStep = { id: SessionProvisionStep["id"]; status: SessionProvisionStep["status"]; startedAt?: string | null; endedAt?: string | null };

/** The wire checklist of an asynchronous start, with absent times as undefined. */
export function toProvisionSteps(steps: WireProvisionStep[] | null | undefined): SessionProvisionStep[] | undefined {
	return steps?.map((step) => ({
		id: step.id,
		status: step.status,
		startedAt: step.startedAt ?? undefined,
		endedAt: step.endedAt ?? undefined,
	}));
}
