import { useMutation, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { settingsQueryKey } from "./useSettings";

/** Changes the app-wide workflow defaults projects inherit (Settings, Reviews). */
export type WorkflowDefaultsPatch = { autoReview?: boolean; planningMode?: "auto" | "always" | "off"; planAutoApprove?: boolean };

export function useUpdateWorkflowDefaults() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (patch: WorkflowDefaultsPatch) => {
			const { data, error } = await apiClient.PATCH("/api/v1/settings/workflow-defaults", { body: patch });
			if (error) throw error;
			return data;
		},
		// Refetch rather than writing the value in locally: the daemon is the source of truth.
		onSuccess: () => queryClient.invalidateQueries({ queryKey: settingsQueryKey }),
	});
	return {
		update: (patch: WorkflowDefaultsPatch) => mutation.mutate(patch),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}
