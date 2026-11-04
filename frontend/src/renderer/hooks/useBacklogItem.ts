import { useQuery } from "@tanstack/react-query";
import { backlogItemKey, getBacklogItem } from "../lib/backlog-api";

/** One item with its links (list reads carry link counts only). */
export function useBacklogItem(projectId: string | undefined, ref: string | undefined) {
	return useQuery({
		queryKey: backlogItemKey(projectId ?? "", ref ?? ""),
		queryFn: () => getBacklogItem(projectId as string, ref as string),
		enabled: Boolean(projectId && ref),
	});
}
