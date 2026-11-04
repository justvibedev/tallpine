import { useMutation } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type RetentionReport = components["schemas"]["CleanupRunReport"];
export type RetentionCategory = components["schemas"]["CleanupCategoryReport"];

/** The cleanup category that applies the session retention policy. */
export const RETENTION_CATEGORY = "session_retention";

/**
 * Runs only the session_retention cleanup category. A dry run previews with
 * the current settings even while the policy is off; a real run purges only
 * projects where it is on.
 */
export function useRetentionRun() {
	const mutation = useMutation({
		mutationFn: async (dryRun: boolean): Promise<RetentionCategory | undefined> => {
			const { data, error } = await apiClient.POST("/api/v1/cleanup/runs", { body: { dryRun, only: [RETENTION_CATEGORY] } });
			if (error || !data) throw error ?? new Error("empty cleanup response");
			return data.categories.find((c) => c.category === RETENTION_CATEGORY);
		},
	});
	return {
		run: mutation.mutateAsync,
		pending: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
		reset: mutation.reset,
	};
}
