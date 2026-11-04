import { useMutation } from "@tanstack/react-query";
import { controlBacklogRun, type BacklogRunControl } from "../lib/backlog-run-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

/** Pause, resume or stop a run; each posts one nudge to the orchestrator. */
export function useControlBacklogRun(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ runRef, action }: { runRef: string; action: BacklogRunControl }) => controlBacklogRun(projectId, runRef, action),
		onSettled: invalidate,
	});
}
