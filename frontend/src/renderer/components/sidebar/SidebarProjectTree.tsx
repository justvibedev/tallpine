import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import { AnimatePresence } from "motion/react";
import { type WorkspaceSummary } from "../../types/workspace";
import { SidebarMenu } from "../ui/sidebar";
import type { RemoteHost } from "../../hooks/useRemoteHosts";
import { sessionNavigateTarget } from "../../lib/navigate-to-session";
import { sessionUiKey } from "../../lib/hosts";
import { RemoteHostsSection } from "../RemoteHostsSection";
import { AnimatedSectionBody } from "./AnimatedSectionBody";
import { CreateProjectListItem } from "./CreateProjectListItem";
import { ProjectItem } from "./ProjectItem";
import { ScratchpadSection } from "./ScratchpadSection";
import { ShowMoreRow } from "./ShowMoreRow";
import { SidebarSectionScroller } from "./SidebarSectionScroller";
import { SECTION_SCROLLER_CLASS } from "./constants";
import { type Selection } from "./types";
import { useProjectDisclosure } from "./useProjectDisclosure";
import { useProjectDragReorder } from "./useProjectDragReorder";
import { useSidebarProjects } from "./useSidebarProjects";

export function SidebarProjectTree({
	workspaceError,
	remoteHosts,
	remoteWorkspaces,
	remoteFailedHostIds,
	selection,
	isIconRail,
	remoteNavigate,
	disclosure,
	projects,
	drag,
	removeProject,
	onOpenRemoteProject,
	onConfigureRemoteProject,
	onRemoveRemoteProject,
	onOpenRemoteOrchestrator,
	onRetryRemoteHosts,
}: {
	workspaceError?: string;
	remoteHosts: RemoteHost[];
	remoteWorkspaces: WorkspaceSummary[];
	remoteFailedHostIds: string[];
	selection: Selection;
	isIconRail: boolean;
	remoteNavigate: ReturnType<typeof useNavigate>;
	disclosure: ReturnType<typeof useProjectDisclosure>;
	projects: ReturnType<typeof useSidebarProjects>;
	drag: ReturnType<typeof useProjectDragReorder>;
	removeProject: (projectId: string) => Promise<void>;
	onOpenRemoteProject: (hostId: string, projectId: string) => void;
	onConfigureRemoteProject: (hostId: string, projectId: string) => void;
	onRemoveRemoteProject: (hostId: string, projectId: string) => Promise<void>;
	onOpenRemoteOrchestrator: (hostId: string, projectId: string) => void;
	onRetryRemoteHosts: () => void;
}) {
	const { t } = useTranslation();
	const {
		initialActiveSessionProjectId,
		expandedIds,
		collapsedRemoteProjects,
		setCollapsedRemoteProjects,
		dismissedInitialActiveProjectIds,
		toggleProjectDisclosure,
		scratchpadOpen,
		setScratchpadOpen,
	} = disclosure;
	const {
		projectWorkspaces,
		standaloneWorkspace,
		visibleWorkspaces,
		hiddenProjectCount,
		showAllProjects,
		toggleShowAllProjects,
		projectContentOpen,
	} = projects;
	const {
		projectDragClickGuard,
		draggingProjectId,
		dropLine,
		handleProjectDragStart,
		handleProjectDragEnd,
		handleProjectDragOver,
		handleProjectDrop,
	} = drag;
	return (
		<>
		{workspaceError ? (
			<div className="sidebar-expanded-chrome px-2.5 py-3 group-data-[collapsible=icon]:hidden">
				<p className="text-sm text-foreground">{t("shell.couldNotLoadProjects")}</p>
				<p className="mt-1 text-caption text-passive">{workspaceError}</p>
			</div>
		) : null}
		{projectWorkspaces.length > 0 || remoteHosts.length > 0 ? (
			<AnimatedSectionBody open={projectContentOpen} className="min-h-0 flex-initial group-data-[collapsible=icon]:flex-none">
				<SidebarSectionScroller
					className={`${SECTION_SCROLLER_CLASS} min-h-0 flex-1`}
					testId="sidebar-projects-scroller"
					wrapperClassName="flex flex-col flex-1"
				>
					<SidebarMenu className="relative gap-0.5 rounded-lg group-data-[collapsible=icon]:gap-1 group-data-[collapsible=icon]:rounded-none">
						<AnimatePresence initial={false}>
							{!workspaceError && visibleWorkspaces.map((workspace) => (
								<ProjectItem
									key={workspace.id}
									workspace={workspace}
									expanded={expandedIds.has(workspace.id) || (initialActiveSessionProjectId === workspace.id && !dismissedInitialActiveProjectIds.has(workspace.id))}
									suppressInitialExpandAnimation={expandedIds.has(workspace.id)}
									selection={selection}
									isDragged={draggingProjectId === workspace.id}
									projectDragInProgress={draggingProjectId !== null}
									consumeDragClick={projectDragClickGuard.consumeClick}
									onToggle={toggleProjectDisclosure}
									onRemoveProject={removeProject}
									onProjectDragStart={handleProjectDragStart}
									onProjectDragEnd={handleProjectDragEnd}
									onProjectDragOver={handleProjectDragOver}
									onProjectDrop={handleProjectDrop}
								/>
							))}
						</AnimatePresence>
						<RemoteHostsSection
							hosts={remoteHosts}
							workspaces={remoteWorkspaces}
							failedHostIds={remoteFailedHostIds}
							renderProject={(host, workspace) => {
								const hostId = host.hostId;
								const projectKey = sessionUiKey(workspace.id, hostId);
								const scopedSelection: Selection = {
									...selection,
									activeProjectId: selection.activeRemoteHostId === hostId ? selection.activeRemoteProjectId : undefined,
									activeSessionId: selection.activeRemoteHostId === hostId ? selection.activeRemoteSessionId : undefined,
								goProject: (projectId) => { onOpenRemoteProject(hostId, projectId); return undefined; },
									goSession: (projectId, sessionId) => { void remoteNavigate(sessionNavigateTarget(projectId, sessionId, hostId)); },
									goSettings: (projectId) => onConfigureRemoteProject(hostId, projectId),
								};
								return <ProjectItem
									key={projectKey}
									workspace={workspace}
									hostLabel={host.label}
									expanded={!collapsedRemoteProjects.has(projectKey)}
									selection={scopedSelection}
									isDragged={false}
									projectDragInProgress={false}
									consumeDragClick={() => false}
									onToggle={() => setCollapsedRemoteProjects((previous) => {
										const next = new Set(previous);
										next.has(projectKey) ? next.delete(projectKey) : next.add(projectKey);
										return next;
									})}
									onRemoveProject={(projectId) => onRemoveRemoteProject(hostId, projectId)}
									onOpenOrchestrator={() => onOpenRemoteOrchestrator(hostId, workspace.id)}
									suppressInitialExpandAnimation
									onProjectDragStart={() => undefined}
									onProjectDragEnd={() => undefined}
									onProjectDragOver={() => undefined}
									onProjectDrop={() => undefined}
								/>;
							}}
							onRetry={onRetryRemoteHosts}
						/>
						{isIconRail && <CreateProjectListItem />}
						<div
							aria-hidden="true"
							data-project-drop-line=""
							className="pointer-events-none absolute inset-x-0 z-[70] h-px rounded-full bg-foreground transition-opacity duration-100"
							style={{ top: dropLine.top, opacity: dropLine.visible ? 1 : 0 }}
						/>
					</SidebarMenu>
				</SidebarSectionScroller>
				{!isIconRail && hiddenProjectCount > 0 ? (
					<ShowMoreRow
						expanded={showAllProjects}
						label={
							showAllProjects
								? t("shell.showLessProjects")
								: t("shell.showMoreProjects", { count: hiddenProjectCount })
						}
							onClick={toggleShowAllProjects}
					/>
				) : null}
			</AnimatedSectionBody>
		) : null}
		{!workspaceError && standaloneWorkspace ? (
			<ScratchpadSection
				workspace={standaloneWorkspace}
				selection={selection}
				isCollapsed={isIconRail}
				open={scratchpadOpen}
				onToggle={() => setScratchpadOpen((open) => !open)}
			/>
		) : null}
		</>
	);
}
