import type { OwnerAsk } from "../../lib/ask-api";
import { planPhase } from "../plan/planDockModel";
import type { ProjectPlan } from "../../lib/plan-api";

/** The open asks that belong in one chat: a lane's chat holds its lane's, the main chat the un-laned ones. */
export function asksForChat(asks: readonly OwnerAsk[], lane: string | undefined): OwnerAsk[] {
	return asks.filter((a) => a.state === "open" && (lane ? a.lane === lane : !a.lane));
}

/** Open lane asks, which the main chat only points to. */
export function laneAsksElsewhere(asks: readonly OwnerAsk[], lane: string | undefined): OwnerAsk[] {
	return lane ? [] : asks.filter((a) => a.state === "open" && a.lane);
}

/** The answered ask a re-asked one repeats, for the "asked again" line. */
export function earlierAsk(asks: readonly OwnerAsk[], ask: OwnerAsk): OwnerAsk | undefined {
	return ask.reaskOf ? asks.find((a) => a.ref === ask.reaskOf) : undefined;
}

/**
 * How many things wait on the owner in one chat: its open asks plus its plans
 * that need an answer or an approval. A plan started in a lane belongs to the
 * lane orchestrator that started it; every other plan to the main chat.
 */
export function waitingCount(
	asks: readonly OwnerAsk[],
	plans: readonly ProjectPlan[],
	chat: { lane?: string; sessionId?: string },
): number {
	const planWaits = plans.filter((p) => {
		const phase = planPhase(p.plan);
		if (phase !== "answering" && phase !== "review") return false;
		return chat.lane ? p.plan.starterSessionId === chat.sessionId : !p.lane;
	}).length;
	return asksForChat(asks, chat.lane).length + planWaits;
}
