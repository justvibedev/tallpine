import { useMutation } from "@tanstack/react-query";
import { updateBacklogItem, type BacklogUpdate } from "../lib/backlog-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

export function useUpdateBacklogItem(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ ref, body }: { ref: string; body: BacklogUpdate }) => updateBacklogItem(projectId, ref, body),
		onSettled: invalidate,
	});
}
