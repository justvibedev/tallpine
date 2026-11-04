import { useQueries } from "@tanstack/react-query";
import { backlogItemKey, getBacklogItem, type BacklogItem } from "../lib/backlog-api";

/**
 * An item the page was pointed at (`?item=`) that the fetched lists do not hold:
 * a closed item past the page cap, or a link to another project's item. The
 * single-item read accepts the id, so ask the projects in scope; only the one
 * that owns it answers.
 */
export function useBacklogItemById(projectIds: readonly string[], itemId: string | undefined, enabled: boolean): BacklogItem | undefined {
	const queries = useQueries({
		queries: projectIds.map((projectId) => ({
			queryKey: backlogItemKey(projectId, itemId ?? ""),
			queryFn: () => getBacklogItem(projectId, itemId as string),
			enabled: enabled && Boolean(itemId),
			retry: false,
		})),
	});
	return queries.find((query) => query.data)?.data;
}
