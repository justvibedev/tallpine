import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";

const NONE: ReadonlySet<string> = new Set();

/**
 * Registry keys the organization's managed config locks (app-wide, or for one
 * project). Settings controls for these keys are read-only; the daemon refuses
 * a write with SETTING_LOCKED either way. Turn it off for a remote host's project: the local daemon's locks do not apply to it. A daemon that cannot answer locks nothing.
 */
export function useManagedLocks(projectId?: string, enabled = true): ReadonlySet<string> {
	const query = useQuery({
		queryKey: ["settings-values", projectId ?? ""],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/values", { params: { query: projectId ? { project: projectId } : {} } });
			if (error) throw error;
			return new Set((data?.values ?? []).filter((v) => v.locked && v.lockedBy === "managed").map((v) => v.key));
		},
		enabled,
		retry: false,
		staleTime: 30_000,
	});
	return query.data ?? NONE;
}
