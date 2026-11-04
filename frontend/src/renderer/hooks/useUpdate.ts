import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type UpdateStatus = components["schemas"]["UpdateStatusResponse"];
export const updateQueryKey = ["update"] as const;

/**
 * Polls the update status. While a job runs (and while the daemon restarts
 * under it) it polls fast, and keeps the last known data so the UI shows the
 * result after reconnecting.
 */
export function useUpdateStatus() {
	return useQuery<UpdateStatus>({
		queryKey: updateQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/update");
			if (error) throw new Error(apiErrorMessage(error));
			if (!data) throw new Error("Update status is unavailable");
			return data;
		},
		refetchInterval: (q) => (q.state.data?.running || q.state.status === "error" ? 3_000 : 60_000),
	});
}

export function useStartUpdate() {
	const client = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (commit: string) => {
			const { data, error } = await apiClient.POST("/api/v1/update", { body: { commit } });
			if (error || !data) throw new Error(apiErrorMessage(error));
			return data;
		},
		onSuccess: () => client.invalidateQueries({ queryKey: updateQueryKey }),
	});
	return {
		start: mutation.mutateAsync,
		starting: mutation.isPending,
		error: mutation.error ? mutation.error.message : undefined,
	};
}
