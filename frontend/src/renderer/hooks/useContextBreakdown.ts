import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { clientForSessionHost } from "../lib/host-clients";

export type ContextBreakdownData = components["schemas"]["ControllersContextBreakdownResponse"];

/**
 * What fills a running agent's context window, asked of the agent when the
 * details open (never polled, never part of the conversation snapshot: the agent
 * may count each category with a model API). `available: false` and a failed
 * request both mean there is no breakdown, and the caller keeps showing totals.
 */
export function useContextBreakdown(sessionId: string | undefined, hostId?: string) {
	const query = useQuery({
		queryKey: ["context-breakdown", hostId ?? "local", sessionId],
		enabled: Boolean(sessionId),
		// A breakdown is a moment in time: every open asks again.
		staleTime: 0,
		gcTime: 0,
		retry: false,
		queryFn: async ({ signal }) => {
			const { data, error } = await clientForSessionHost(hostId).GET(
				"/api/v1/sessions/{sessionId}/conversation/context-breakdown",
				{ params: { path: { sessionId: sessionId as string } }, signal },
			);
			if (error) throw error;
			return data;
		},
	});
	return {
		breakdown: query.data?.available ? query.data : undefined,
		loading: query.isPending,
	};
}
