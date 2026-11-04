import { useMutation, useQueryClient } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import { conversationQueryKey } from "./conversation";
import { apiErrorMessage } from "../lib/api-client";

/**
 * Restarts a chat's agent in place so a setting read only at launch, such as the
 * auto-compaction window, takes effect. The daemon keeps the conversation and
 * resumes the provider's own history; it refuses (409) while the agent has work
 * in flight, which `busy` reports so the caller can say so rather than retry.
 */
export function useRestartAgent(sessionId: string | undefined, hostId?: string) {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async () => {
			const { error, response } = await clientForSessionHost(hostId).POST(
				"/api/v1/sessions/{sessionId}/conversation/restart-agent",
				{ params: { path: { sessionId: sessionId as string } } },
			);
			if (error) throw Object.assign(new Error(apiErrorMessage(error)), { status: response.status });
			return true;
		},
		onSettled: () => queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId as string, hostId) }),
	});
	return {
		restart: () => mutation.mutate(),
		restarting: mutation.isPending,
		busy: (mutation.error as { status?: number } | null)?.status === 409,
		error: mutation.error ? mutation.error.message : undefined,
	};
}
