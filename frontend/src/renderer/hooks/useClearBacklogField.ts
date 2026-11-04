import { useMutation } from "@tanstack/react-query";
import { clearBacklogField } from "../lib/backlog-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

export function useClearBacklogField(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ ref, key }: { ref: string; key: string }) => clearBacklogField(projectId, ref, key),
		onSettled: invalidate,
	});
}
