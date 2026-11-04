import { useTranslation } from "react-i18next";
import { useEffect, memo, useRef, useState, type MouseEvent } from "react";
import { motion, useReducedMotion } from "motion/react";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { ContextMenu, ContextMenuTrigger } from "../ui/context-menu";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { sessionUiKey } from "../../lib/hosts";
import { ProjectContextMenuContent } from "./ProjectContextMenuContent";
import { ProjectRemoveDialog } from "./ProjectRemoveDialog";
import { ProjectRowActions } from "./ProjectRowActions";
import { ProjectRowButton } from "./ProjectRowButton";
import { ProjectSessionsList } from "./ProjectSessionsList";
import { type ProjectItemProps, projectItemPropsEqual } from "./project-item-props";
import { useProjectOrchestrator } from "./useProjectOrchestrator";
import { useProjectRemoval } from "./useProjectRemoval";
import { useProjectSessions } from "./useProjectSessions";

export const ProjectItem = memo(function ProjectItem({
	workspace,
	hostLabel,
	onOpenOrchestrator,
	expanded,
	selection,
	isDragged,
	projectDragInProgress,
	consumeDragClick,
	onToggle,
	onRemoveProject,
	suppressInitialExpandAnimation,
	onProjectDragStart,
	onProjectDragEnd,
	onProjectDragOver,
	onProjectDrop,
}: ProjectItemProps) {

	const { t } = useTranslation();
	const nameWithHost = hostLabel ? `${workspace.name} · ${hostLabel}` : workspace.name;
	const isStandalone = workspace.id === STANDALONE_WORKSPACE_ID;
	const prefersReducedMotion = useReducedMotion();
	const activeProjectMatches = selection.activeProjectId === workspace.id;
	const projectHeaderRef = useRef<HTMLDivElement>(null);
	const projectSessionsRef = useRef<HTMLDivElement>(null);
	const dashboardActive = activeProjectMatches && !selection.activeSessionId;
	const orchestratorActive =
		activeProjectMatches &&
		workspace.sessions.some(
			(session) => session.id === selection.activeSessionId && session.kind === "orchestrator",
		);

	// Skip enter animation on first mount — sessions arrive async and we don't
	// want them to slide in on every sidebar load. Only animate on subsequent
	// expand/collapse toggles.
	const [animReady, setAnimReady] = useState(false);
	const hasInteractedWithDisclosure = useRef(false);
	useEffect(() => {
		const id = window.setTimeout(() => setAnimReady(true), 500);
		return () => window.clearTimeout(id);
	}, []);
	const projectKey = sessionUiKey(workspace.id, workspace.hostId);
	const isProjectProvisioning = useUiStore((state) => state.provisioningProjectIds.has(projectKey));
	const projectActive = dashboardActive || orchestratorActive || (activeProjectMatches && isProjectProvisioning);
	const isProjectRestarting = useUiStore((state) => state.restartingProjectIds.has(projectKey));
	const projectIsDragging = isDragged;
	const sessionList = useProjectSessions({ workspace, selection, projectKey, expanded, activeProjectMatches, projectHeaderRef, projectSessionsRef });
	const { sessions } = sessionList;
	const toggleDisclosure = () => {
		hasInteractedWithDisclosure.current = true;
		onToggle(workspace.id);
	};
	const { orchestrator, isSpawning, openOrchestrator } = useProjectOrchestrator({ workspace, selection, expanded, toggleDisclosure, onOpenOrchestrator, isProjectProvisioning, isProjectRestarting });

	// Expanded + already on the project board → collapse. Expanded + on a
	// session (orchestrator or worker) → board. Collapsed → expand + board.
	// Do not treat orchestratorActive like the board: the project row is the
	// one-click path back from the orchestrator button.
	const onProjectClick = () => {
		if (consumeDragClick(workspace.id)) return;
		if (isStandalone) {
			toggleDisclosure();
			return;
		}
		if (!expanded) {
			toggleDisclosure();
			selection.goProject(workspace.id);
		} else if (dashboardActive) {
			toggleDisclosure();
		} else {
			selection.goProject(workspace.id);
		}
	};

	// Folder icon always toggles disclosure, even when another project is
	// selected — without this, collapsing a non-active project required a
	// select click then a second click (felt like a double-click).
	const onFolderClick = (event: MouseEvent) => {
		event.stopPropagation();
		if (consumeDragClick(workspace.id)) return;
		toggleDisclosure();
	};
	const { removeError, isRemoving, confirmOpen, setConfirmOpen, removeProject, handleConfirmRemove } = useProjectRemoval({ workspace, selection, onRemoveProject });

	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<motion.li
					className={cn(
						"group/menu-item relative group-data-[collapsible=icon]:mb-0",
						projectIsDragging && "opacity-50",
					)}
					data-dragging={projectIsDragging ? "true" : undefined}
					data-project-drop-target=""
					data-project-id={workspace.id}
					data-remote-project-row={workspace.hostId ? "" : undefined}
					data-host-id={workspace.hostId}
					data-sidebar="menu-item"
					data-slot="sidebar-menu-item"
					initial={false}
					animate={{ opacity: 1, y: 0 }}
					exit={{ opacity: 0, y: -4, transition: { duration: prefersReducedMotion ? 0 : 0.12, ease: "easeIn" } }}
					onDragOver={(event) => onProjectDragOver(event, workspace.id)}
					onDrop={onProjectDrop}
					transition={prefersReducedMotion ? { duration: 0 } : { type: "spring", stiffness: 520, damping: 42, mass: 0.55 }}
				>
					<div
						ref={projectHeaderRef}
						className={cn(
							"relative",
							activeProjectMatches && "sticky top-0 z-20 bg-sidebar group-data-[collapsible=icon]:static",
						)}
						data-project-drag-row=""
						data-project-id={workspace.id}
						draggable={!workspace.hostId}
						onDragStart={(event) => onProjectDragStart(event, workspace.id)}
						onDragEnd={onProjectDragEnd}
					>
						<div className={cn("relative", projectIsDragging && "cursor-grabbing")}>
							<ProjectRowButton
								workspace={workspace}
								hostLabel={hostLabel}
								nameWithHost={nameWithHost}
								isStandalone={isStandalone}
								expanded={expanded}
								dashboardActive={dashboardActive}
								projectActive={projectActive}
								projectIsDragging={projectIsDragging}
								projectDragInProgress={projectDragInProgress}
								onProjectClick={onProjectClick}
								onFolderClick={onFolderClick}
							/>
							{/* Per-project actions: orchestrator and kebab menu. Outside the row's
							    navigation surface so their own presses stay independent. */}
							<ProjectRowActions
								workspace={workspace}
								isStandalone={isStandalone}
								nameWithHost={nameWithHost}
								selection={selection}
								orchestrator={orchestrator}
								orchestratorActive={orchestratorActive}
								isSpawning={isSpawning}
								isProjectProvisioning={isProjectProvisioning}
								isProjectRestarting={isProjectRestarting}
								isRemoving={isRemoving}
								projectDragInProgress={projectDragInProgress}
								openOrchestrator={openOrchestrator}
								removeProject={removeProject}
							/>
						</div>
						{/* end outer relative */}
					</div>
					{isRemoving ? (
						<div className="sidebar-expanded-chrome px-5 py-1 text-2xs text-muted-foreground" role="status">
							{t("shell.removingNamed", { name: workspace.name })}
						</div>
					) : removeError ? (
						<div className="sidebar-expanded-chrome px-5 py-1 text-2xs text-destructive" role="alert">
							{removeError}
						</div>
					) : null}
					{/* project-sidebar__sessions: indented under the project parent so worker
					    sessions read as children without adding a persistent guide rail. */}
					<ProjectSessionsList
						expanded={expanded}
						hasSessions={sessions.length > 0}
						projectSessionsRef={projectSessionsRef}
						animReady={animReady}
						suppressInitialExpandAnimation={suppressInitialExpandAnimation}
						hasInteractedWithDisclosure={hasInteractedWithDisclosure}
						projectKey={projectKey}
						projectDragInProgress={projectDragInProgress}
						selection={selection}
						sessionList={sessionList}
					/>
					<ProjectRemoveDialog workspace={workspace} open={confirmOpen} onOpenChange={setConfirmOpen} onConfirm={handleConfirmRemove} />
				</motion.li>
			</ContextMenuTrigger>
			<ProjectContextMenuContent workspace={workspace} selection={selection} isStandalone={isStandalone} isProjectRestarting={isProjectRestarting} isRemoving={isRemoving} removeProject={removeProject} />
		</ContextMenu>
	);
}, projectItemPropsEqual);
