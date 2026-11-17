import { apiErrorCode } from "./api-client";
import { clientForSessionHost } from "./host-clients";
import type { HostId } from "./hosts";

/**
 * How many more times one Resume press waits on a daemon resume that is still
 * running. The daemon answers AGENT_RESUME_RUNNING after waiting about 45 s
 * while the resume itself keeps going for up to 3 minutes, so a few re-joins
 * cover the whole attempt and end on its real result.
 */
export const RESUME_REJOIN_LIMIT = 4;

/**
 * POST resume-agent, re-joining while the daemon reports the resume is still
 * running. Each re-join attaches to the same daemon attempt; it never starts a
 * second one. Returns the last response unchanged.
 */
export async function postResumeAgent(sessionId: string, hostId?: HostId) {
	for (let attempt = 0; ; attempt++) {
		const result = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/resume-agent", {
			params: { path: { sessionId } },
		});
		if (!result.error || apiErrorCode(result.error) !== "AGENT_RESUME_RUNNING" || attempt >= RESUME_REJOIN_LIMIT) {
			return result;
		}
	}
}
