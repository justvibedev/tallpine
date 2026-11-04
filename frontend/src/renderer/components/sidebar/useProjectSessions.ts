import { useQuery } from "@tanstack/react-query";
import { backlogListKey, listBacklog } from "../../lib/backlog-api";
import { useCallback, useEffect, useLayoutEffect, useMemo, useState, type RefObject } from "react";
import { type WorkspaceSession, type WorkspaceSummary, sortedWorkerSessions, resolveNextNavigationAfterSessionKill, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { useLatestCallback, useStableArray } from "../../hooks/useStable";
import { useSidebarOrderStore } from "../../stores/sidebar-order-store";
import { applyOrder } from "../../lib/sidebar-order";
import { SIDEBAR_PROJECT_SESSION_LIMIT } from "./constants";
import { type Selection } from "./types";
import { useShowMoreCap } from "./useShowMoreCap";

export function useProjectSessions({ workspace, selection, projectKey, expanded, activeProjectMatches, projectHeaderRef, projectSessionsRef }: { workspace: WorkspaceSummary; selection: Selection; projectKey: string; expanded: boolean; activeProjectMatches: boolean; projectHeaderRef: RefObject<HTMLDivElement | null>; projectSessionsRef: RefObject<HTMLDivElement | null> }) {
	// Keep completed PR sessions reachable while their runtime still exists.
	// Only termination removes a worker from the sidebar; archived sessions stay
	// reachable through SessionsBoard.
	// Lane orchestrators sit under the project row (which holds the default
	// orchestrator), before the workers, titled by their lane item.
	const laneSessions = useMemo(
		() => workspace.sessions.filter((session) => session.kind === "orchestrator" && session.lane && session.isTerminated !== true),
		[workspace.sessions],
	);
	const laneItems = useQuery({
		queryKey: backlogListKey(workspace.id, "open"),
		queryFn: () => listBacklog(workspace.id, "open"),
		enabled: laneSessions.length > 0,
	});
	const visibleSessions = useMemo(() => {
		const titles = new Map((laneItems.data ?? []).map((item) => [item.ref, item.title]));
		const lanes = laneSessions.map((session) => ({ ...session, title: titles.get(session.lane ?? "") ?? session.lane ?? session.title }));
		return [...lanes, ...sortedWorkerSessions(workspace.sessions).filter((session) => session.isTerminated !== true)];
	}, [laneSessions, laneItems.data, workspace.sessions]);
	const [sessionOrder, setSessionOrder] = useState<string[]>([]);
	const sessions = useMemo(
		() => applyOrder(visibleSessions, (session) => session.id, sessionOrder, "start"),
		[sessionOrder, visibleSessions],
	);
	// Publish the order shown here so keyboard navigation follows it.
	useEffect(() => {
		const sessionIds = sessions.map((session) => session.id);
		useSidebarOrderStore.getState().setSessionOrder(projectKey, sessionIds);
		return () => useSidebarOrderStore.getState().clearSessionOrder(projectKey);
	}, [projectKey, sessions]);
	const {
		listed: listedSessions,
		hiddenCount: hiddenSessionCount,
		showAll: showAllSessions,
		toggleShowAll: toggleShowAllSessions,
	} = useShowMoreCap(sessions, SIDEBAR_PROJECT_SESSION_LIMIT, selection.activeSessionId);
	useLayoutEffect(() => {
		const header = projectHeaderRef.current;
		const content = projectSessionsRef.current;
		const scroller = header?.closest<HTMLElement>("[data-sidebar-section-scroller]");
		if (!activeProjectMatches || !expanded || !header || !content || !scroller) return;
		// Keep the sticky header clear while preventing session rows from painting
		// through it. Scroll/resize only changes the clip, not the list's layout.
		const updateClip = () => {
			const covered = Math.max(0, header.getBoundingClientRect().bottom - content.getBoundingClientRect().top);
			content.style.clipPath = `inset(${covered}px 0 0 0)`;
		};
		updateClip();
		scroller.addEventListener("scroll", updateClip, { passive: true });
		const observer = typeof ResizeObserver === "undefined" ? undefined : new ResizeObserver(updateClip);
		observer?.observe(header);
		observer?.observe(content);
		return () => {
			scroller.removeEventListener("scroll", updateClip);
			observer?.disconnect();
			content.style.removeProperty("clip-path");
		};
	}, [activeProjectMatches, expanded, showAllSessions]);
	// Stable while the ids are: a rebuilt array would invalidate every sortable row.
	const listedSessionIds = useStableArray(useMemo(() => listedSessions.map((session) => session.id), [listedSessions]));
	const commitSessionOrder = useCallback(
		(next: string[] | null) => {
			if (!next) return;
			// Only the listed slice is draggable, so keep the still-hidden tail
			// behind it rather than letting applyOrder float it to the front.
			const listed = new Set(next);
			setSessionOrder([...next, ...sessions.filter((session) => !listed.has(session.id)).map((session) => session.id)]);
		},
		[sessions],
	);
	// Stable row handlers: selection changes on every session switch, and a new
	// handler would re-render every row (with its menus, tooltips, and dialogs).
	const openSession = useLatestCallback((sessionId: string) => {
		selection.goSession(workspace.id, sessionId);
	});
	const handleSessionKilled = useLatestCallback((killedSession: WorkspaceSession) => {
		if (selection.activeSessionId !== killedSession.id) return;
		const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id, sessions);
		if (nextRoute.target === "session") {
			selection.goSession(workspace.id, nextRoute.sessionId);
		} else if (workspace.id === STANDALONE_WORKSPACE_ID) {
			selection.goHome();
		} else {
			selection.goProject(workspace.id);
		}
	});

	return {
		sessions,
		listedSessions,
		listedSessionIds,
		hiddenSessionCount,
		showAllSessions,
		toggleShowAllSessions,
		commitSessionOrder,
		openSession,
		handleSessionKilled,
	};
}
