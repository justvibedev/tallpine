import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type SilentFailures = components["schemas"]["SilentFailuresResponse"];
export const silentFailuresQueryKey = ["health", "silent-failures"] as const;

/** Query options kept exported so a test can pin the polling contract. */
export const silentFailuresPolling = {
	refetchIntervalInBackground: true,
	refetchOnWindowFocus: "always",
	staleTime: 0,
} as const;

/** Polls the daemon for conditions that fail without any visible error. */
export function useSilentFailures() {
	return useQuery<SilentFailures>({
		queryKey: silentFailuresQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/health/silent-failures");
			if (error) throw new Error(apiErrorMessage(error));
			if (!data) throw new Error("Health status is unavailable");
			return data;
		},
		// Slow poll; back off further while the endpoint itself is failing.
		// Keep polling while the window is backgrounded and refresh on focus so a
		// cleared condition never leaves a stale badge.
		...silentFailuresPolling,
		refetchInterval: (q) => (q.state.status === "error" ? 120_000 : 30_000),
	});
}
