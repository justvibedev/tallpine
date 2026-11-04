import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef } from "react";
import { projectBoardNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { findProjectOrchestrator, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";

type Options = {
	sessionId: string;
	/** Project named by the route, if any. */
	projectId?: string;
	/** The open session as the views resolve it; undefined once it is no longer there. */
	session: WorkspaceSession | undefined;
	/** The lookups behind `session` have finished, so undefined is an answer and not a load. */
	settled: boolean;
	workspaces: WorkspaceSummary[];
	enabled: boolean;
};

/**
 * A session the app has shown and the daemon no longer has (an ended lane
 * orchestrator, a deleted session) is gone, not an error: leave it for the
 * project's default orchestrator, or the project board, so no view keeps
 * polling it and none shows its 404. A route to an id that was never seen
 * still reports "not found".
 */
export function useSessionGoneRedirect({ sessionId, projectId, session, settled, workspaces, enabled }: Options): boolean {
	const navigate = useNavigate();
	const seen = useRef(new Map<string, string | undefined>());
	if (session) seen.current.set(sessionId, session.workspaceId ?? projectId);
	const gone = enabled && settled && !session && seen.current.has(sessionId);
	const project = seen.current.get(sessionId) ?? projectId;
	useEffect(() => {
		if (!gone) return;
		const fallback = project ? findProjectOrchestrator(workspaces, project) : undefined;
		if (fallback && fallback.id !== sessionId) void navigate(sessionNavigateTarget(project, fallback.id));
		else void navigate(project ? projectBoardNavigateTarget(project) : { to: "/" });
	}, [gone, navigate, project, sessionId, workspaces]);
	return gone;
}
