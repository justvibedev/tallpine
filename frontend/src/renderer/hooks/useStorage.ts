/**
 * Everything Tallpine and its agents keep on disk (Settings > Storage). The
 * daemon measures in the background and serves the cached result, so this
 * polls while a measurement is running.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type StorageReport = components["schemas"]["StorageResponse"];
export type StorageEntry = components["schemas"]["StorageEntry"];
export type StorageProcess = components["schemas"]["StorageProcess"];
export type StorageCleanupReport = components["schemas"]["CleanupRunReport"];

export const storageQueryKey = ["storage"] as const;
const POLL_MS = 2000;

export function useStorage() {
	const query = useQuery({
		queryKey: storageQueryKey,
		retry: false,
		refetchInterval: (q) => (q.state.data?.refreshing ? POLL_MS : false),
		queryFn: async (): Promise<StorageReport> => {
			const { data, error } = await apiClient.GET("/api/v1/storage", { params: { query: {} } });
			if (error || !data) throw error ?? new Error("empty storage response");
			return data;
		},
	});
	const queryClient = useQueryClient();
	const refresh = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/storage", { params: { query: { refresh: true } } });
			if (error || !data) throw error ?? new Error("empty storage response");
			return data;
		},
		onSuccess: (data) => queryClient.setQueryData(storageQueryKey, data),
	});
	return {
		report: query.data,
		isLoading: query.isLoading,
		error: query.error ? apiErrorMessage(query.error) : undefined,
		refresh: () => refresh.mutate(),
	};
}

export function useDeleteStorageEntry() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (id: string) => {
			const { error } = await apiClient.POST("/api/v1/storage/delete", { body: { id } });
			if (error) throw error;
		},
		onSettled: () => queryClient.invalidateQueries({ queryKey: storageQueryKey }),
	});
	return {
		remove: mutation.mutateAsync,
		pending: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
		reset: mutation.reset,
	};
}

export function useCleanStorage() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (dryRun: boolean) => {
			const { data, error } = await apiClient.POST("/api/v1/storage/cleanup", { body: { dryRun } });
			if (error || !data) throw error ?? new Error("empty cleanup response");
			return data;
		},
		onSuccess: (_data, dryRun) => (dryRun ? undefined : queryClient.invalidateQueries({ queryKey: storageQueryKey })),
	});
	return {
		clean: mutation.mutateAsync,
		pending: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
		reset: mutation.reset,
	};
}
