import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { appSkillsQueryKey } from "../lib/custom-skills";
import type { SkillRoles } from "../lib/project-local-fields";

/** The app-wide rules and skill switches every project inherits (B740). */
export type AppInstructions = { workerRules: string; orchestratorRules: string; reviewerRules: string; skillRoles: SkillRoles };

/** The registry key of each field. */
export const APP_INSTRUCTION_KEYS = {
	workerRules: "app.workerRules",
	orchestratorRules: "app.orchestratorRules",
	reviewerRules: "app.reviewerRules",
	skillRoles: "app.skillRoles",
} as const satisfies Record<keyof AppInstructions, string>;

const appInstructionsQueryKey = ["app-instructions"] as const;

/** Reads the app instructions from the settings registry and saves any subset of them. */
export function useAppInstructions() {
	const client = useQueryClient();
	const query = useQuery({
		queryKey: appInstructionsQueryKey,
		queryFn: async (): Promise<AppInstructions> => {
			const { data, error } = await apiClient.GET("/api/v1/settings/values", { params: { query: {} } });
			if (error) throw new Error(apiErrorMessage(error));
			const value = (key: string) => data?.values.find((v) => v.key === key)?.value;
			const text = (key: string) => (typeof value(key) === "string" ? (value(key) as string) : "");
			const roles = value(APP_INSTRUCTION_KEYS.skillRoles);
			return {
				workerRules: text(APP_INSTRUCTION_KEYS.workerRules),
				orchestratorRules: text(APP_INSTRUCTION_KEYS.orchestratorRules),
				reviewerRules: text(APP_INSTRUCTION_KEYS.reviewerRules),
				skillRoles: roles && typeof roles === "object" ? (roles as SkillRoles) : {},
			};
		},
	});
	const save = useMutation({
		mutationFn: async (patch: Partial<AppInstructions>) => {
			const set = Object.fromEntries(Object.entries(patch).map(([field, v]) => [APP_INSTRUCTION_KEYS[field as keyof AppInstructions], v]));
			const { error } = await apiClient.PATCH("/api/v1/settings/values", { body: { set } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		// Every project page shows the app switches it inherits: refetch them too.
		onSettled: () => Promise.all([appInstructionsQueryKey, appSkillsQueryKey, ["project-skills"]].map((queryKey) => client.invalidateQueries({ queryKey }))),
	});
	return {
		instructions: query.data,
		loadError: query.error instanceof Error ? query.error.message : undefined,
		save: save.mutate,
		saving: save.isPending,
		saveError: save.error instanceof Error ? save.error.message : undefined,
	};
}
