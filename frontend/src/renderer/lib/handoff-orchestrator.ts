import { apiErrorCode, apiErrorMessage } from "./api-client";
import { clientForSessionHost } from "./host-clients";

/** The message that asks a running orchestrator to write its state note and replace itself. */
export const HANDOFF_REQUEST_MESSAGE =
	"Hand off now: write your state note (exact SHAs, worker-to-PR mapping, pending reviews, open decisions) " +
	"into your artifact directory, then run `tallpine orchestrator handoff --note <path to that note>`. " +
	"A fresh orchestrator will read it and take over; workers keep running.";

export type HandoffOutcome =
	| { kind: "handedOff"; sessionId: string; projectId: string }
	| { kind: "noNote" };

/** Replace the orchestrator with a fresh one seeded from its newest state note. */
export async function handoffOrchestratorWithLatestNote(orchestratorId: string, hostId?: string): Promise<HandoffOutcome> {
	const { data, error, response } = await clientForSessionHost(hostId).POST("/api/v1/orchestrators/{id}/handoff", {
		params: { path: { id: orchestratorId } },
		body: {},
	});
	if (error && apiErrorCode(error) === "HANDOFF_NOTE_REQUIRED") return { kind: "noNote" };
	if (error || !data?.orchestrator?.id) {
		throw new Error(apiErrorMessage(error, `Failed to hand off the orchestrator (${response.status})`));
	}
	return { kind: "handedOff", sessionId: data.orchestrator.id, projectId: data.orchestrator.projectId };
}

/** Ask the running orchestrator to write its note and hand off itself. */
export async function askOrchestratorToHandOff(orchestratorId: string, hostId?: string): Promise<void> {
	const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/send", {
		params: { path: { sessionId: orchestratorId } },
		body: { message: HANDOFF_REQUEST_MESSAGE, userAuthored: true },
	});
	if (error) throw new Error(apiErrorMessage(error, "Unable to message the orchestrator"));
}
