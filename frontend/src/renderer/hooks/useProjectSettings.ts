import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { fetchProject, projectQueryKey } from "../lib/project-settings-api";

/** One project's loaded settings (local daemon, or a remote host's when `hostId` is given). */
export function useProjectSettings(projectId: string, { hostId, enabled = true }: { hostId?: string; enabled?: boolean } = {}) {
	const { t } = useTranslation();
	return useQuery({
		queryKey: projectQueryKey(projectId, hostId),
		enabled,
		queryFn: () => fetchProject(projectId, t("settings.project.degraded"), hostId),
	});
}
