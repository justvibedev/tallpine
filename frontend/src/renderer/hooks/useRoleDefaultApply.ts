import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type DefaultRole = "orchestrator" | "worker" | "reviewer";
export type RoleDefaultDriftReport = components["schemas"]["ProjectRoleDefaultDriftReport"];
export type RoleDefaultApplyResult = components["schemas"]["ProjectApplyRoleDefaultResult"];

const driftKey = (role: DefaultRole) => ["settings", "role-default-drift", role] as const;

/**
 * The projects whose role does not run with the app-wide default (B847), read
 * only while `enabled` (the confirm dialog is open), and the action that
 * brings the listed ones in line.
 */
export function useRoleDefaultApply(role: DefaultRole, enabled: boolean) {
	const queryClient = useQueryClient();
	const drift = useQuery({
		queryKey: driftKey(role),
		enabled,
		// Always compare against the projects as they are now.
		staleTime: 0,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/role-defaults/{role}/drift", { params: { path: { role } } });
			if (error) throw error;
			return data;
		},
	});
	const apply = useMutation({
		mutationFn: async (projectIds: string[]) => {
			const { data, error } = await apiClient.POST("/api/v1/settings/role-defaults/{role}/apply", { params: { path: { role } }, body: { projectIds } });
			if (error) throw error;
			return data;
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: driftKey(role) });
			// Project settings views read the role overrides this rewrote.
			void queryClient.invalidateQueries({ queryKey: ["project"] });
		},
	});
	return {
		report: drift.data,
		loading: drift.isLoading,
		loadError: drift.error ? apiErrorMessage(drift.error) : undefined,
		apply: (projectIds: string[]) => apply.mutateAsync(projectIds),
		applying: apply.isPending,
		applyError: apply.error ? apiErrorMessage(apply.error) : undefined,
		resetApply: () => apply.reset(),
	};
}
