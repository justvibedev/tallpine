import { useMutation } from "@tanstack/react-query";
import { removeBacklogLink, type BacklogLinkKind } from "../lib/backlog-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

export function useRemoveBacklogLink(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ ref, kind, target }: { ref: string; kind: BacklogLinkKind; target: string }) => removeBacklogLink(projectId, ref, kind, target),
		onSettled: invalidate,
	});
}
