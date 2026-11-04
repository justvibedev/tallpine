import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { clientForSessionHost } from "../lib/host-clients";

export type ModelUsageData = components["schemas"]["ControllersModelUsageResponse"];

/**
 * A session's cumulative token usage by model, read when the context details
 * open. A failed request reads as no usage: the popover keeps its other rows.
 */
export function useModelUsage(sessionId: string | undefined, hostId?: string) {
	const query = useQuery({
		queryKey: ["model-usage", hostId ?? "local", sessionId],
		enabled: Boolean(sessionId),
		staleTime: 0,
		gcTime: 0,
		retry: false,
		queryFn: async ({ signal }) => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/usage", {
				params: { path: { sessionId: sessionId as string } },
				signal,
			});
			if (error) throw error;
			return data;
		},
	});
	return { models: query.data?.models ?? [] };
}
