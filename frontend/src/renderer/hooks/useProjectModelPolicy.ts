import { useQuery } from "@tanstack/react-query";
import { clientForSessionHost } from "../lib/host-clients";
import type { ModelPolicy } from "../lib/model-policy";

/** The project's model policy, so pickers can leave out models the daemon would refuse. */
export function useProjectModelPolicy(projectId: string | undefined, hostId?: string): ModelPolicy | undefined {
	return useQuery({
		queryKey: ["project-model-policy", hostId ?? "", projectId ?? ""],
		enabled: Boolean(projectId),
		staleTime: 60_000,
		queryFn: async (): Promise<ModelPolicy> => {
			const { data } = await clientForSessionHost(hostId).GET("/api/v1/projects/{id}", { params: { path: { id: projectId ?? "" } } });
			const project = data?.status === "ok" ? data.project : undefined;
			return (project && "config" in project ? project.config?.models : undefined) ?? {};
		},
	}).data;
}
