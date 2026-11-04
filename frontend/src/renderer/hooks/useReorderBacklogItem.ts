import { useMutation, useQueryClient } from "@tanstack/react-query";
import { backlogListKey, reorderBacklogItem, type BacklogItem } from "../lib/backlog-api";
import type { BacklogReorderTarget } from "../lib/backlog-reorder";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

/**
 * Drag reorder. The open list is reordered in the cache at once so the row
 * lands where it was dropped, and restored if the server refuses; the refetch
 * after settling brings the authoritative positions.
 */
export function useReorderBacklogItem(projectId: string) {
	const client = useQueryClient();
	const invalidate = useBacklogInvalidate(projectId);
	const key = backlogListKey(projectId, "open");
	return useMutation({
		mutationFn: ({ ref, target }: { ref: string; target: BacklogReorderTarget }) => reorderBacklogItem(projectId, ref, target),
		onMutate: async ({ ref, target }) => {
			await client.cancelQueries({ queryKey: key });
			const previous = client.getQueryData<BacklogItem[]>(key);
			if (previous) client.setQueryData<BacklogItem[]>(key, moveInList(previous, ref, target));
			return { previous };
		},
		onError: (_error, _vars, context) => {
			if (context?.previous) client.setQueryData(key, context.previous);
		},
		onSettled: invalidate,
	});
}

function moveInList(list: readonly BacklogItem[], ref: string, target: BacklogReorderTarget): BacklogItem[] {
	const ordered = [...list].sort((a, b) => a.position - b.position);
	const from = ordered.findIndex((item) => item.ref === ref);
	const anchorRef = "before" in target ? target.before : target.after;
	if (from < 0 || !ordered.some((item) => item.ref === anchorRef)) return [...list];
	const [moved] = ordered.splice(from, 1);
	const anchor = ordered.findIndex((item) => item.ref === anchorRef);
	ordered.splice("before" in target ? anchor : anchor + 1, 0, moved);
	return ordered.map((item, index) => ({ ...item, position: index }));
}
