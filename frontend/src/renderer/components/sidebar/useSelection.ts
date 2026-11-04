import { useNavigate, useParams } from "@tanstack/react-router";
import { useCallback, useMemo } from "react";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { useUiStore } from "../../stores/ui-store";

// Selection state comes from the URL: which project/session is active is the
// route params, and clicks navigate rather than mutate a store.
export function useSelection() {
	const navigate = useNavigate();
	const openProjectSettings = useUiStore((state) => state.openProjectSettings);
	const params = useParams({ strict: false }) as {
		hostId?: string;
		projectId?: string;
		sessionId?: string;
	};
	const goHome = useCallback(() => void navigate({ to: "/" }), [navigate]);
	const goStandaloneBoard = useCallback(() => void navigate({ to: "/sessions" }), [navigate]);
	const goSettings = useCallback((projectId: string) => openProjectSettings(projectId), [openProjectSettings]);
	const goProject = useCallback(
		(projectId: string) => void navigate({ to: "/projects/$projectId", params: { projectId } }),
		[navigate],
	);
	const goSession = useCallback(
		(projectId: string, sessionId: string) => {
			if (projectId === STANDALONE_WORKSPACE_ID) {
				void navigate({ to: "/sessions/$sessionId", params: { sessionId } });
				return;
			}
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId },
			});
		},
		[navigate],
	);
	return useMemo(() => ({
		activeRemoteHostId: params.hostId,
		activeRemoteProjectId: params.hostId ? params.projectId : undefined,
		activeRemoteSessionId: params.hostId ? params.sessionId : undefined,
		activeProjectId: params.hostId ? undefined : params.projectId,
		activeSessionId: params.hostId ? undefined : params.sessionId,
		goHome,
		goStandaloneBoard,
		goSettings,
		goProject,
		goSession,
	}), [goHome, goProject, goSession, goSettings, goStandaloneBoard, params.hostId, params.projectId, params.sessionId]);
}
