import { useMutation } from "@tanstack/react-query";
import { createBacklogItem } from "../lib/backlog-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

export function useCreateBacklogItem(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: (input: { title: string; note?: string }) => createBacklogItem(projectId, input),
		onSettled: invalidate,
	});
}
