import { useMutation } from "@tanstack/react-query";
import { addBacklogLink, type BacklogLinkKind } from "../lib/backlog-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

export function useAddBacklogLink(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ ref, kind, target }: { ref: string; kind: BacklogLinkKind; target: string }) => addBacklogLink(projectId, ref, kind, target),
		onSettled: invalidate,
	});
}
