import { useQuery } from "@tanstack/react-query";
import { backlogItemUsageKey, getBacklogItemUsage } from "../lib/backlog-run-api";

/** Cost and elapsed time of an item's linked sessions. */
export function useBacklogItemUsage(projectId: string, ref: string, enabled: boolean) {
	return useQuery({
		queryKey: backlogItemUsageKey(projectId, ref),
		queryFn: () => getBacklogItemUsage(projectId, ref),
		enabled,
	});
}
