import { useQueries } from "@tanstack/react-query";
import { BACKLOG_PAGE_LIMIT, backlogListKey, listBacklog, type BacklogItem, type BacklogState } from "../lib/backlog-api";

const STATES: readonly BacklogState[] = ["inbox", "open", "done", "dropped"];

/**
 * Every item of the given projects in every state. One request per project and
 * state keeps each response under the API's page limit; the page groups them.
 * Writes and `backlog_changed` events invalidate by project, so the lists stay
 * live without polling. The list endpoint has no cursor, so a response that
 * fills the page is reported as `truncated`: closed items only ever accumulate,
 * and what is missing would otherwise go unnoticed.
 */
export function useBacklogItems(projectIds: readonly string[]) {
	const queries = useQueries({
		queries: projectIds.flatMap((projectId) =>
			STATES.map((state) => ({ queryKey: backlogListKey(projectId, state), queryFn: () => listBacklog(projectId, state) })),
		),
	});
	const items: BacklogItem[] = queries.flatMap((query) => query.data ?? []);
	const error = queries.find((query) => query.error)?.error ?? null;
	return {
		items,
		error: error as Error | null,
		truncated: queries.some((query) => (query.data?.length ?? 0) >= BACKLOG_PAGE_LIMIT),
		isLoading: queries.some((query) => query.isPending),
		refetch: () => Promise.all(queries.map((query) => query.refetch())),
	};
}
