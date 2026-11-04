import { useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { agentAccountsQueryKey } from "./useAgentAccounts";

export type AccountAdoption = components["schemas"]["AccountAdoptionResponse"];
export type AccountAdoptionItem = components["schemas"]["AccountAdoptionItemResponse"];
export type AccountAdoptionSession = components["schemas"]["AccountAdoptionSession"];
export type AccountAdoptionPolicy = components["schemas"]["AdoptAgentAccountRequest"]["policy"];

const previewKey = (accountId: string) => ["agent-accounts", "adopt-preview", accountId] as const;
const batchKey = (accountId: string, batchId: string) => ["agent-accounts", "adoption", accountId, batchId] as const;

function unwrap<T>({ data, error }: { data?: T; error?: unknown }): T {
	if (error) throw new Error(apiErrorMessage(error));
	return data as T;
}

/** The live sessions that "Use for all sessions" would move. Refused accounts
 * (signed out, split history) fail here, before the user confirms. */
export function useAdoptionPreview(accountId: string, enabled: boolean) {
	return useQuery({
		queryKey: previewKey(accountId),
		enabled,
		retry: false,
		gcTime: 0,
		queryFn: async () => unwrap(await apiClient.GET("/api/v1/agents/accounts/{accountId}/adopt/preview", { params: { path: { accountId } } })).sessions,
	});
}

/** A batch's per-session outcomes, read every two seconds while it runs. */
export function useAdoptionBatch(accountId: string, batchId: string | undefined, initial?: AccountAdoption) {
	return useQuery({
		queryKey: batchKey(accountId, batchId ?? ""),
		enabled: Boolean(batchId),
		initialData: initial,
		queryFn: async () => unwrap(await apiClient.GET("/api/v1/agents/accounts/{accountId}/adoptions/{batchId}", { params: { path: { accountId, batchId: batchId ?? "" } } })),
		refetchInterval: (query) => (query.state.data?.state === "completed" ? false : 2000),
	});
}

/** Starts the switch. The same requestKey is a replay on the daemon, so a
 * double click or a retry never queues a second batch. */
export function useAdoptAgentAccount() {
	const queryClient = useQueryClient();
	return async (accountId: string, policy: AccountAdoptionPolicy, requestKey: string): Promise<AccountAdoption> => {
		const batch = unwrap(await apiClient.POST("/api/v1/agents/accounts/{accountId}/adopt", {
			params: { path: { accountId } }, body: { policy, requestKey },
		}));
		await queryClient.invalidateQueries({ queryKey: agentAccountsQueryKey });
		return batch;
	};
}
