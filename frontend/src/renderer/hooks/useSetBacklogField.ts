import { useMutation } from "@tanstack/react-query";
import { setBacklogField } from "../lib/backlog-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

export function useSetBacklogField(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ ref, key, value }: { ref: string; key: string; value: string }) => setBacklogField(projectId, ref, key, value),
		onSettled: invalidate,
	});
}
