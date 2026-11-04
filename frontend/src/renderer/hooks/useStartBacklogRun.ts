import { useMutation } from "@tanstack/react-query";
import { startBacklogRun, type BacklogRunScope, type BacklogRunSelection } from "../lib/backlog-run-api";
import { useBacklogInvalidate } from "./useBacklogInvalidate";

/** Creates a run; the daemon then messages the orchestrator once. */
export function useStartBacklogRun(projectId: string) {
	const invalidate = useBacklogInvalidate(projectId);
	return useMutation({
		mutationFn: ({ scope, selection }: { scope: BacklogRunScope; selection: BacklogRunSelection }) => startBacklogRun(projectId, scope, selection),
		onSettled: invalidate,
	});
}
