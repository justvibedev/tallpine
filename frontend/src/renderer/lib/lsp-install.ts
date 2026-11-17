import { useEffect, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";
import { projectSkillsQueryKey } from "./skills-offer";

export type LspInstallJob = components["schemas"]["ControllersProjectLSPInstallJob"];
export type LspInstallTarget = LspInstallJob["target"];

export const lspInstallQueryKey = (projectId: string) => ["project-lsp-install", projectId] as const;

export async function fetchLspInstall(projectId: string): Promise<LspInstallJob | undefined> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/skills/lsp/install", { params: { path: { id: projectId } } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data.job;
}

/** Starts an install by catalog ids only; the daemon picks the command. */
export async function startLspInstall(projectId: string, language: string, target: LspInstallTarget): Promise<LspInstallJob | undefined> {
	const { data, error } = await apiClient.POST("/api/v1/projects/{id}/skills/lsp/install", { params: { path: { id: projectId } }, body: { language, target } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data.job;
}

/**
 * The project's LSP install job: polls while one runs, and re-reads the skills
 * inventory when it finishes so the installed/missing status flips.
 */
export function useLspInstall(projectId: string) {
	const client = useQueryClient();
	const key = lspInstallQueryKey(projectId);
	const query = useQuery({
		queryKey: key, retry: false,
		queryFn: () => fetchLspInstall(projectId),
		refetchInterval: (q) => (q.state.data?.state === "running" ? 1000 : false),
	});
	const start = useMutation({
		mutationFn: ({ language, target }: { language: string; target: LspInstallTarget }) => startLspInstall(projectId, language, target),
		onSuccess: (job) => client.setQueryData(key, job),
	});
	const job = query.data;
	const wasRunning = useRef(false);
	useEffect(() => {
		const running = job?.state === "running";
		if (wasRunning.current && !running) void client.invalidateQueries({ queryKey: projectSkillsQueryKey(projectId) });
		wasRunning.current = running;
	}, [job?.state, projectId, client]);
	return { job, running: job?.state === "running" || start.isPending, start: start.mutate, startError: start.error?.message, startFor: start.variables };
}
