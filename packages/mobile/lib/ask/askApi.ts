import { req } from "../api";
import type { ServerConfig } from "../config";
import type { OwnerAsk } from "./askModel";

const base = (projectId: string) => `/api/v1/projects/${encodeURIComponent(projectId)}/asks`;

/** The project's asks, all states: the strip shows the open ones and a re-asked card finds the earlier answer among the rest. */
export async function listProjectAsks(cfg: ServerConfig, projectId: string): Promise<OwnerAsk[]> {
	const res = await req(cfg, `${base(projectId)}?state=all`);
	const data = await res.json();
	return Array.isArray(data?.asks) ? data.asks : [];
}

/** The owner's answer: a chosen option id, or free text ("Other"). */
export type AskAnswerInput = { option: string } | { other: string };

export async function answerAsk(cfg: ServerConfig, projectId: string, ref: string, input: AskAnswerInput): Promise<{ ask: OwnerAsk; deliveryFailed: boolean }> {
	const res = await req(cfg, `${base(projectId)}/${encodeURIComponent(ref)}/answer`, { method: "POST", body: JSON.stringify(input) });
	const data = await res.json();
	return { ask: data.ask, deliveryFailed: data.deliveryFailed === true };
}
