import { useMemo } from "react";
import { useUnreadNotificationCount } from "../../hooks/useUnreadNotificationCount";
import { artifactCount, groupArtifactsByProject } from "../../lib/artifacts";
import { sessionWantsAttention } from "../../lib/session-presentation";
import { workerSessions, type WorkspaceSummary } from "../../types/workspace";

/** Counts shown on rail icons: sessions waiting on the user, artifacts, and unread notifications. */
export function useRailBadges(workspaces: WorkspaceSummary[]) {
	const inbox = useUnreadNotificationCount();
	return useMemo(() => ({
		attention: workspaces
			.flatMap((workspace) => workerSessions(workspace.sessions))
			.filter((session) => sessionWantsAttention(session)).length,
		artifacts: artifactCount(groupArtifactsByProject(workspaces)),
		inbox,
	}), [inbox, workspaces]);
}
