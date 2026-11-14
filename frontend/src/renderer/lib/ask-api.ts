import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type OwnerAsk = components["schemas"]["OwnerAsk"];

/** Under the project's backlog key, so a backlog_changed event (kind "ask") refetches it. */
export const projectAsksKey = (projectId: string) => ["backlog", projectId, "asks"] as const;

function fail(error: unknown): never {
	throw new Error(apiErrorMessage(error));
}

const ASKS = "/api/v1/projects/{projectId}/asks" as const;
const ANSWER = "/api/v1/projects/{projectId}/asks/{ref}/answer" as const;

/**
 * The project's asks, oldest first. All states: the strip shows the open ones
 * and a re-asked card looks up the earlier answer among the rest.
 */
export async function listProjectAsks(projectId: string): Promise<OwnerAsk[]> {
	const { data, error } = await apiClient.GET(ASKS, { params: { path: { projectId }, query: { state: "all" } } });
	if (error) fail(error);
	return data?.asks ?? [];
}

/** The owner's answer: a chosen option id, or free text ("Other"). */
export type AskAnswerInput = { option: string; other?: undefined } | { other: string; option?: undefined };

/** deliveryFailed: recorded, but no live orchestrator got the reply. */
export async function answerAsk(projectId: string, ref: string, input: AskAnswerInput): Promise<{ ask: OwnerAsk; deliveryFailed: boolean }> {
	const { data, error } = await apiClient.POST(ANSWER, { params: { path: { projectId, ref } }, body: input });
	if (error || !data) fail(error);
	return { ask: data.ask, deliveryFailed: data.deliveryFailed === true };
}
