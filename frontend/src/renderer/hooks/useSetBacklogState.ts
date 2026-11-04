import { useMutation } from "@tanstack/react-query";
import { setBacklogState, type BacklogState } from "../lib/backlog-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

export function useSetBacklogState(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ ref, state, reason }: { ref: string; state: BacklogState; reason?: string }) => setBacklogState(projectId, ref, state, reason),
		onSettled: invalidate,
	});
}
