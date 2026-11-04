import { apiErrorMessage } from "./api-client";
import { clientForSessionHost } from "./host-clients";

export type EndLaneResult = {
	/** Orchestrator the lane's live workers moved to; empty for a dry run. */
	orchestrator: string;
	/** Live workers that moved (dry run: that would move). */
	workers: string[];
};

/**
 * Fully remove a lane orchestrator and hand its live workers to the default
 * orchestrator. With `dryRun` nothing changes: the daemon returns the workers
 * that would move, or refuses when the orchestrator's worktree holds unsaved work.
 */
export async function endLane(orchestratorId: string, options: { dryRun?: boolean; hostId?: string } = {}): Promise<EndLaneResult> {
	const { data, error, response } = await clientForSessionHost(options.hostId).POST("/api/v1/orchestrators/end", {
		body: { sessionId: orchestratorId, dryRun: options.dryRun || undefined },
	});
	if (error || !data) {
		throw new Error(apiErrorMessage(error, `Failed to end the lane (${response.status})`));
	}
	return { orchestrator: data.orchestrator, workers: data.workers ?? [] };
}
