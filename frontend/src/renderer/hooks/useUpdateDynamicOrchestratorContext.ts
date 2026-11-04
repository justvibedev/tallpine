import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { settingsQueryKey } from "./useSettings";

/** Changes the global "Dynamic orchestrator context" toggle (orchestrator research mode). */
export function useUpdateDynamicOrchestratorContext() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (enabled: boolean) => {
			const { data, error } = await apiClient.PATCH("/api/v1/settings/dynamic-orchestrator-context", { body: { enabled } });
			if (error) throw error;
			return data;
		},
		// Refetch rather than writing the value in locally: the daemon is the source of truth.
		onSuccess: () => queryClient.invalidateQueries({ queryKey: settingsQueryKey }),
	});
	return {
		update: (enabled: boolean) => mutation.mutate(enabled),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}
