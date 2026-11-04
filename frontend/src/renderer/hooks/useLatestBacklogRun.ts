import { useQuery } from "@tanstack/react-query";
import { backlogRunKey, getLatestBacklogRun } from "../lib/backlog-run-api";

/** The project's newest run with its plan. Live through `backlog_changed`, which invalidates the project's backlog keys. */
export function useLatestBacklogRun(projectId: string | undefined, enabled: boolean) {
	return useQuery({
		queryKey: backlogRunKey(projectId ?? ""),
		queryFn: () => getLatestBacklogRun(projectId as string),
		enabled: enabled && Boolean(projectId),
	});
}
