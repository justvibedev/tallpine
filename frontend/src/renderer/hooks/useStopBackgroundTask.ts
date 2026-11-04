import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { workspaceQueryKeyForHost } from "./useWorkspaceQuery";

/** Stops one background task of a session's agent; the task list refreshes once it ends. */
export function useStopBackgroundTask(sessionId: string, hostId?: string) {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (taskId: string) => {
			const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/background-tasks/stop", {
				params: { path: { sessionId } },
				body: { taskId },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSettled: () => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) }),
	});
	return {
		stop: (taskId: string) => mutation.mutate(taskId),
		stoppingId: mutation.isPending ? mutation.variables : undefined,
		error: mutation.error?.message,
	};
}
