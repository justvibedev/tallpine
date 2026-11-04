import { useLayoutEffect, useMemo } from "react";
import { type WorkspaceSummary, STANDALONE_PROJECT_KIND } from "../../types/workspace";
import { useStableArray } from "../../hooks/useStable";
import { useSidebarOrderStore } from "../../stores/sidebar-order-store";
import { applyOrder } from "../../lib/sidebar-order";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import { SIDEBAR_INITIAL_SECTION_LIMIT } from "./constants";
import { type Selection } from "./types";
import { useShowMoreCap } from "./useShowMoreCap";

export function useSidebarProjects({ workspaces, remoteHosts, selection, projectsOpen, isIconRail }: { workspaces: WorkspaceSummary[]; remoteHosts: RemoteHost[]; selection: Selection; projectsOpen: boolean; isIconRail: boolean }) {
	const projectOrder = useSidebarOrderStore((state) => state.projectOrder);
	const pendingCreatedProjectIds = useSidebarOrderStore((state) => state.pendingCreatedProjectIds);

	useLayoutEffect(() => {
		// Preserve absent IDs in place: creation can publish its order before the
		// query snapshot arrives. Reconcile before paint so a late snapshot cannot
		// briefly show older, newly discovered projects above that creation.
		useSidebarOrderStore.getState().reconcileProjectOrder(workspaces.map((workspace) => workspace.id));
	}, [workspaces, pendingCreatedProjectIds]);
	const orderedWorkspaces = useMemo(
		() => applyOrder(workspaces, (workspace) => workspace.id, projectOrder, "start"),
		[projectOrder, workspaces],
	);

	// The ad hoc group is a bucket for projectless sessions, not a project: it
	// gets its own Scratchpad section below the project list rather than a
	// project row appended to the end of it.
	const projectWorkspaces = useMemo(
		() => orderedWorkspaces.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND),
		[orderedWorkspaces],
	);
	const standaloneWorkspace = useMemo(
		() => workspaces.find((workspace) => workspace.kind === STANDALONE_PROJECT_KIND),
		[workspaces],
	);
	const {
		listed: visibleWorkspaces,
		hiddenCount: hiddenProjectCount,
		showAll: showAllProjects,
		toggleShowAll: toggleShowAllProjects,
	} = useShowMoreCap(projectWorkspaces, SIDEBAR_INITIAL_SECTION_LIMIT, selection.activeProjectId, isIconRail);
	const projectContentOpen = (projectWorkspaces.length > 0 || remoteHosts.length > 0) && (projectsOpen || isIconRail);
	// Stable while the ids are, so the drag handlers built on it (props of every
	// memoized project row) survive a list write that only changed a session.
	const projectIds = useStableArray(useMemo(
		() => projectWorkspaces.map((workspace) => workspace.id),
		[projectWorkspaces],
	));

	return {
		projectWorkspaces,
		standaloneWorkspace,
		visibleWorkspaces,
		hiddenProjectCount,
		showAllProjects,
		toggleShowAllProjects,
		projectContentOpen,
		projectIds,
	};
}
