/**
 * Enterprise agent policy. The daemon resolves managed config, environment, the
 * project override and the user's own setting, and describes every rule and
 * setting; the renderer renders rows from that metadata and writes the user layer
 * (a setting locked by managed config or the environment refuses the write).
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";

export type AgentPolicy = components["schemas"]["AgentPolicyResponse"];
export type AgentPolicyRule = components["schemas"]["AgentPolicyRule"];
export type AgentPolicyField = components["schemas"]["AgentPolicyField"];

const agentPolicyQueryKey = ["settings", "agent-policy"] as const;

const keyFor = (projectId?: string) => (projectId ? ([...agentPolicyQueryKey, projectId] as const) : agentPolicyQueryKey);

/** The install's policy, or one project's view of it (with that project's overrides) when projectId is given. */
export function useAgentPolicy(projectId?: string, enabled = true) {
	const query = useQuery({
		enabled,
		queryKey: keyFor(projectId),
		queryFn: async (): Promise<AgentPolicy> => {
			const { data, error } = projectId
				? await apiClient.GET("/api/v1/projects/{id}/agent-policy", { params: { path: { id: projectId } } })
				: await apiClient.GET("/api/v1/settings/agent-policy");
			if (error) throw error;
			return data as AgentPolicy;
		},
	});
	return { policy: query.data, isLoading: query.isLoading };
}

/** alsoClear names sibling fields reset to "" in the same write (dependent pickers). */
export type AgentPolicyChange = { rule: string; field: string; value: unknown; alsoClear?: string[] };

/** A multi-rule write: one PATCH carrying several fields (e.g. both mode switches). */
export type AgentPolicyRules = Record<string, Record<string, unknown>>;

export function useUpdateAgentPolicy(projectId?: string) {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (change: AgentPolicyChange | { rules: AgentPolicyRules }) => {
			const body =
				"rules" in change
					? { rules: change.rules }
					: { rules: { [change.rule]: { ...Object.fromEntries((change.alsoClear ?? []).map((name) => [name, ""])), [change.field]: change.value } } };
			const { data, error } = projectId
				? await apiClient.PATCH("/api/v1/projects/{id}/agent-policy", { params: { path: { id: projectId } }, body })
				: await apiClient.PATCH("/api/v1/settings/agent-policy", { body });
			if (error) throw error;
			return data;
		},
		// The daemon is the source of truth; refetch so a refused (locked) write is
		// never shown as saved.
		onSuccess: () => queryClient.invalidateQueries({ queryKey: agentPolicyQueryKey }),
	});
	return {
		update: (change: AgentPolicyChange) => mutation.mutate(change),
		updateRules: (rules: AgentPolicyRules) => mutation.mutate({ rules }),
		saving: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}

/** True when managed config or the environment sets the setting, so the user cannot change it. */
export function isPolicyFieldLocked(field: Pick<AgentPolicyField, "locked">): boolean {
	return field.locked;
}

/** A row is shown unless it names another setting of the rule that does not have the required value. */
export function isPolicyFieldVisible(rule: AgentPolicyRule, field: AgentPolicyField): boolean {
	const cond = field.visibleWhen;
	if (!cond) return true;
	const other = rule.fields.find((f) => f.name === cond.field);
	return String(other?.value) === cond.equals;
}
