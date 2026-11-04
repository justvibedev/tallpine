import { useNavigate } from "@tanstack/react-router";
import { useCallback, useMemo } from "react";
import { type WorkspaceSession, type WorkspaceSummary, resolveNextNavigationAfterSessionKill, workerSessions, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { useLatestCallback } from "../../hooks/useStable";
import { sessionNavigateTarget } from "../../lib/navigate-to-session";
import { type Selection } from "./types";

export function usePinnedSessions({ workspaces, remoteWorkspaces, selection, remoteNavigate, onOpenRemoteProject }: { workspaces: WorkspaceSummary[]; remoteWorkspaces: WorkspaceSummary[]; selection: Selection; remoteNavigate: ReturnType<typeof useNavigate>; onOpenRemoteProject: (hostId: string, projectId: string) => void }) {
	const pinnedSessions = useMemo(
		() => [...workspaces, ...remoteWorkspaces]
			.flatMap((w) => workerSessions(w.sessions))
			.filter((s) => s.isPinned && s.isTerminated !== true)
			.sort((a, b) => {
				const aTime = a.pinnedAt ? new Date(a.pinnedAt).getTime() : 0;
				const bTime = b.pinnedAt ? new Date(b.pinnedAt).getTime() : 0;
				return bTime - aTime;
			}),
		[workspaces, remoteWorkspaces],
	);

	const openPinnedSession = useLatestCallback((target: WorkspaceSession) => {
		if (target.hostId) void remoteNavigate(sessionNavigateTarget(target.workspaceId, target.id, target.hostId));
		else selection.goSession(target.workspaceId, target.id);
	});
	const handlePinnedSessionKilled = useCallback(
		(killedSession: WorkspaceSession) => {
			if (killedSession.hostId) {
				if (selection.activeRemoteHostId !== killedSession.hostId || selection.activeRemoteSessionId !== killedSession.id) return;
				const workspace = remoteWorkspaces.find((candidate) => candidate.hostId === killedSession.hostId && candidate.id === killedSession.workspaceId);
				const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id);
				if (nextRoute.target === "session") void remoteNavigate(sessionNavigateTarget(killedSession.workspaceId, nextRoute.sessionId, killedSession.hostId));
				else if (killedSession.workspaceId === STANDALONE_WORKSPACE_ID) selection.goHome();
				else onOpenRemoteProject(killedSession.hostId, killedSession.workspaceId);
				return;
			}
			if (selection.activeSessionId !== killedSession.id) return;
			const workspace = workspaces.find((w) => w.id === killedSession.workspaceId);
			const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id);
			if (nextRoute.target === "session") {
				selection.goSession(killedSession.workspaceId, nextRoute.sessionId);
			} else {
				selection.goProject(killedSession.workspaceId);
			}
		},
		[onOpenRemoteProject, remoteNavigate, remoteWorkspaces, selection, workspaces],
	);

	return { pinnedSessions, openPinnedSession, handlePinnedSessionKilled };
}
