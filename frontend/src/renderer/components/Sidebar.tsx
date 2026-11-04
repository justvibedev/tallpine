import { useTranslation } from "react-i18next";
import { useNavigate, useRouterState } from "@tanstack/react-router";
import { useCallback, useLayoutEffect, useMemo, useState } from "react";
import { STANDALONE_PROJECT_KIND } from "../types/workspace";
import { useCommandPaletteEnabled } from "../hooks/useCommandPaletteEnabled";
import { useLatestCallback } from "../hooks/useStable";
import { useShellMaybe } from "../lib/shell-context";
import { Sidebar as SidebarRoot, SidebarContent, SidebarGroup, SidebarGroupContent, SidebarRail, useSidebar } from "./ui/sidebar";
import { cn } from "../lib/utils";
import { useSettingsPage } from "./SettingsDialog";
import { InboxList } from "./inbox/InboxList";
import { useUiStore } from "../stores/ui-store";
import { ResizeHandle } from "./ResizeHandle";
import { SettingsSidebarNav } from "./sidebar/SettingsSidebarNav";
import { SessionsPanelHeader } from "./sidebar/SessionsPanelHeader";
import { SidebarFooterNav } from "./sidebar/SidebarFooterNav";
import { SidebarPinnedSection } from "./sidebar/SidebarPinnedSection";
import { SidebarProjectTree } from "./sidebar/SidebarProjectTree";
import { SidebarProjectsHeader } from "./sidebar/SidebarProjectsHeader";
import { SidebarTopNav } from "./sidebar/SidebarTopNav";
import { noDragStyle } from "./sidebar/constants";
import { SIDEBAR_COLLAPSIBLE } from "./sidebar/sizing";
import { type SidebarProps } from "./sidebar/types";
import { usePinnedSessions } from "./sidebar/usePinnedSessions";
import { useProjectDisclosure } from "./sidebar/useProjectDisclosure";
import { useProjectDragReorder } from "./sidebar/useProjectDragReorder";
import { useSelection } from "./sidebar/useSelection";
import { useSidebarProjects } from "./sidebar/useSidebarProjects";
import { useSidebarResize } from "./sidebar/useSidebarResize";
import { NO_WORKSPACES, useSidebarWorkspaces } from "./sidebar/useSidebarWorkspaces";

export {
	resolveNextNavigationAfterSessionKill,
	type NextSessionNavigation,
} from "../types/workspace";
export { sessionDndId } from "./sidebar/reorder-dnd";
export { SIDEBAR_DEFAULT_WIDTH, SIDEBAR_MAX_WIDTH, SIDEBAR_MIN_WIDTH } from "./sidebar/sizing";
export { SettingsSidebarNav } from "./sidebar/SettingsSidebarNav";

// Built on shadcn's sidebar primitives (components/ui/sidebar): the provider in
// _shell owns the persistent open state. Collapsed sidebars move fully off-canvas.

export function Sidebar({
	underTopbar = true,
	topbarOffset = "toolbar",
	workspaceError,
	workspaces: listedWorkspaces,
	remoteHosts = [],
	onCreateRemoteProject,
	onInitializeRemoteProject,
	onOpenRemoteProject = () => undefined,
	onOpenRemoteOrchestrator = () => undefined,
	onConfigureRemoteProject = () => undefined,
	onRemoveRemoteProject = async () => undefined,
	onRetryRemoteHosts = () => undefined,
	remoteWorkspaces: listedRemoteWorkspaces = NO_WORKSPACES,
	remoteFailedHostIds = [],
	onCloneProject,
	onCreateProject,
	onInitializeProject,
	onRemoveProject,
	resizeAuxiliaryTargetRef,
}: SidebarProps) {
	const workspaces = useSidebarWorkspaces(listedWorkspaces);
	const remoteWorkspaces = useSidebarWorkspaces(listedRemoteWorkspaces);
	const { t } = useTranslation();
	const remoteNavigate = useNavigate();
	const selection = useSelection();
	const { state, setOpen, toggleSidebar } = useSidebar();
	const isCollapsed = state === "collapsed";
	// List behavior (force-open sections, lifted Show more caps) belongs to the
	// icon rail only; toggling an offcanvas sidebar must not change any list.
	const isIconRail = isCollapsed && SIDEBAR_COLLAPSIBLE === "icon";
	const [expandedChromeVisible, setExpandedChromeVisible] = useState(!isCollapsed);
	// Daemon status for the smoke suite's sr-only mirror in the footer. Null when
	// rendered outside the shell (unit tests) — the mirror simply doesn't render.
	const daemonStatus = useShellMaybe()?.daemonStatus ?? null;
	const commandPaletteEnabled = useCommandPaletteEnabled();
	const settingsPage = useSettingsPage();
	const inboxActive = useRouterState({ select: (state) => state.location.pathname.startsWith("/inbox") });
	const setCommandPaletteOpen = useUiStore((s) => s.setCommandPaletteOpen);
	const isNarrowShell = useUiStore((s) => s.isNarrowShell);
	const existingProjectPaths = useMemo(
		() => workspaces
			.filter((workspace) => workspace.kind !== STANDALONE_PROJECT_KIND)
			.map((workspace) => workspace.path)
			.filter((path): path is string => Boolean(path)),
		[workspaces],
	);
	const openExistingProject = useCallback(
		(path: string) => {
			const workspace = workspaces.find(
				(candidate) => candidate.kind !== STANDALONE_PROJECT_KIND && candidate.path === path,
			);
			if (workspace) selection.goProject(workspace.id);
		},
		[selection, workspaces],
	);
	useLayoutEffect(() => {
		// Offcanvas: the panel slides off-screen on collapse — no need to hide content.
		// Reveal immediately on expand so there's no fade-in delay.
		if (!isCollapsed) {
			setExpandedChromeVisible(true);
		}
	}, [isCollapsed]);

	const disclosure = useProjectDisclosure({ selection, isCollapsed });
	const { pinnedOpen, setPinnedOpen, projectsOpen, setProjectsOpen } = disclosure;
	// tallpine's sidebar resize: drag the right edge (200-420px,
	// persisted), double-click to reset to 240px. See useSidebarResize.
	const { resizeScopeRef, onResizePointerDown, onCollapsedResizePointerDown, onResizeDoubleClick } = useSidebarResize({ resizeAuxiliaryTargetRef, setOpen });
	const projects = useSidebarProjects({ workspaces, remoteHosts, selection, projectsOpen, isIconRail });
	const drag = useProjectDragReorder(projects.projectIds);
	// The shell rebuilds its remove handler whenever the workspace list changes,
	// which under busy agents is several times a second. Hand the memoized project
	// rows a stable one, or every project re-renders on every list write.
	const removeProject = useLatestCallback(onRemoveProject);
	const { pinnedSessions, openPinnedSession, handlePinnedSessionKilled } = usePinnedSessions({ workspaces, remoteWorkspaces, selection, remoteNavigate, onOpenRemoteProject });

	return (
		// Pinned sidebars start below shell chrome.
		<SidebarRoot
			collapsible={SIDEBAR_COLLAPSIBLE}
			overlay={isNarrowShell}
			resizeScopeRef={resizeScopeRef}
			data-expanded-chrome={expandedChromeVisible ? "visible" : "hidden"}
			data-topbar-offset={underTopbar ? topbarOffset : undefined}
			className={cn(
				"sidebar-focusless",
				// Keep the transparent 1px border on every route so navigation
				// cannot change the content width. Match it with the left gutter.
				"pl-px border-transparent",
				// Prefer top/bottom over h-svh/inset-y so titlebar offset (`top-(--sidebar-chrome-offset)`)
				// clears chrome without fighting a second height constraint.
				!underTopbar
					? "top-0 bottom-0"
					: "top-(--sidebar-chrome-offset) bottom-0 h-auto!",
			)}
		>
			<SessionsPanelHeader selection={selection} settingsOpen={Boolean(settingsPage) || inboxActive} toggleSidebar={toggleSidebar} />

			{settingsPage ? (
				<SettingsSidebarNav layer={settingsPage} />
			) : inboxActive ? (
				<InboxList />
			) : (
			<>
			{/* Keep Search + section chrome fixed above the scrollable sidebar content. */}
			<div className="flex shrink-0 flex-col gap-0 px-2 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
				<SidebarTopNav commandPaletteEnabled={commandPaletteEnabled} setCommandPaletteOpen={setCommandPaletteOpen} />

				<SidebarPinnedSection pinnedSessions={pinnedSessions} pinnedOpen={pinnedOpen} setPinnedOpen={setPinnedOpen} selection={selection} remoteHosts={remoteHosts} openPinnedSession={openPinnedSession} handlePinnedSessionKilled={handlePinnedSessionKilled} />

				<SidebarProjectsHeader projectsOpen={projectsOpen} setProjectsOpen={setProjectsOpen} existingProjectPaths={existingProjectPaths} remoteHosts={remoteHosts} onCreateRemoteProject={onCreateRemoteProject} onInitializeRemoteProject={onInitializeRemoteProject} onCloneProject={onCloneProject} onCreateProject={onCreateProject} onInitializeProject={onInitializeProject} openExistingProject={openExistingProject} />
			</div>

			<SidebarContent className="scrollbar-none gap-0 px-2 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
				<SidebarGroup className="min-h-0 flex-1 p-0">
					{/* Tree (project-sidebar__tree) */}
					<SidebarGroupContent
						className="sidebar-sections-container grid min-h-0 flex-1 auto-rows-[minmax(0,auto)] content-start group-data-[collapsible=icon]:flex group-data-[collapsible=icon]:flex-col"
					>
						<SidebarProjectTree workspaceError={workspaceError} remoteHosts={remoteHosts} remoteWorkspaces={remoteWorkspaces} remoteFailedHostIds={remoteFailedHostIds} selection={selection} isIconRail={isIconRail} remoteNavigate={remoteNavigate} disclosure={disclosure} projects={projects} drag={drag} removeProject={removeProject} onOpenRemoteProject={onOpenRemoteProject} onConfigureRemoteProject={onConfigureRemoteProject} onRemoveRemoteProject={onRemoveRemoteProject} onOpenRemoteOrchestrator={onOpenRemoteOrchestrator} onRetryRemoteHosts={onRetryRemoteHosts} />
					</SidebarGroupContent>
				</SidebarGroup>
			</SidebarContent>
			</>
			)}

			<SidebarFooterNav daemonStatus={daemonStatus} settingsPage={settingsPage} isCollapsed={isCollapsed} />

			{/* useResizable owns clamp. */}
			<ResizeHandle
				className="group-data-[state=collapsed]:hidden"
				onDoubleClick={onResizeDoubleClick}
				onPointerDown={onResizePointerDown}
				side="right"
				style={noDragStyle}
			/>
			<SidebarRail
				aria-label={t("shell.expandSidebar")}
				className="group-data-[state=expanded]:hidden hover:after:bg-transparent"
				onClick={() => setOpen(true)}
				onPointerDown={onCollapsedResizePointerDown}
			/>
		</SidebarRoot>
	);
}
