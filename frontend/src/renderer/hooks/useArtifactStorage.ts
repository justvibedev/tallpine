/**
 * Artifact disk usage and the user-confirmed retention action. The daemon
 * measures and acts; nothing here archives or deletes without an explicit call.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type ArtifactStorageReport = components["schemas"]["ArtifactStorageResponse"];
export type ArtifactStorageSession = components["schemas"]["ArtifactStorageSession"];
export type ArtifactRetentionResponse = components["schemas"]["ArtifactRetentionResponse"];
export type ArtifactRetentionAction = "archive" | "delete";

export const artifactStorageQueryKey = ["artifact-storage"] as const;

/**
 * Measuring walks every session's artifact directory, so it only runs while
 * enabled. The dialog measures fresh each time; a long-lived caller (the
 * Artifacts tab) passes a staleTime and turns off focus refetch, since a
 * retention run invalidates the report.
 */
export function useArtifactStorage(
	olderThanDays: number,
	enabled: boolean,
	options: { staleTime?: number; refetchOnWindowFocus?: boolean } = {},
) {
	const query = useQuery({
		queryKey: [...artifactStorageQueryKey, olderThanDays],
		enabled,
		staleTime: options.staleTime ?? 0,
		refetchOnWindowFocus: options.refetchOnWindowFocus ?? true,
		retry: false,
		queryFn: async (): Promise<ArtifactStorageReport> => {
			const { data, error } = await apiClient.GET("/api/v1/artifacts/storage", {
				params: { query: { olderThanDays } },
			});
			if (error || !data) throw error ?? new Error("empty artifact storage response");
			return data;
		},
	});
	return {
		report: query.data,
		isLoading: query.isLoading,
		error: query.error ? apiErrorMessage(query.error) : undefined,
	};
}

export function useRetainArtifacts() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (body: { action: ArtifactRetentionAction; olderThanDays: number; sessionIds: string[] }) => {
			const { data, error } = await apiClient.POST("/api/v1/artifacts/storage/retention", {
				body: { ...body, confirm: true },
			});
			if (error || !data) throw error ?? new Error("empty artifact retention response");
			return data;
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: artifactStorageQueryKey }),
	});
	return {
		retain: mutation.mutateAsync,
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
		reset: mutation.reset,
	};
}
