import type { NeedsInputAudience, SessionActivity, SessionStatus } from "./session-models";
import { attentionZone, isWaitingOnOrchestrator, sessionWantsAttention } from "./session-presentation";

export type BoardAttentionFacts = {
	activity?: SessionActivity;
	displayStatus?: string;
	needsInputAudience?: NeedsInputAudience;
	scmStatus?: SessionStatus;
	status: SessionStatus;
	statusPresentation?: unknown;
	statusReadiness?: "checking" | "ready" | "unavailable";
};

/** Board card attention chip: sessionWantsAttention plus the daemon's display-status phrases. */
export function boardSessionNeedsAttention(session: BoardAttentionFacts): boolean {
	if (session.statusReadiness && session.statusReadiness !== "ready") return false;
	if (session.statusPresentation) return false;
	switch (session.displayStatus) {
		case "Blocked":
		case "CI failing":
		case "Changes requested":
			return !isWaitingOnOrchestrator(session);
		case undefined:
			return (
				// The Ready lane already marks merge-ready cards; the chip is for human actions.
				(sessionWantsAttention(session) && attentionZone(session.status) !== "merge") ||
				(session.activity?.state === "blocked" && !isWaitingOnOrchestrator(session))
			);
		default:
			return false;
	}
}
