import { useTranslation } from "react-i18next";
import { GitBranch, MoreVertical, Plus, Settings, Trash2 } from "lucide-react";
import { type WorkspaceSession, type WorkspaceSummary } from "../../types/workspace";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { OrchestratorIcon } from "../icons";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { HOVER_ACTION_CLASS, ROW_ACTIONS_CLASS } from "./constants";
import { type Selection } from "./types";

export function ProjectRowActions({ workspace, isStandalone, nameWithHost, selection, orchestrator, orchestratorActive, isSpawning, isProjectProvisioning, isProjectRestarting, isRemoving, projectDragInProgress, openOrchestrator, removeProject }: { workspace: WorkspaceSummary; isStandalone: boolean; nameWithHost: string; selection: Selection; orchestrator: WorkspaceSession | undefined; orchestratorActive: boolean; isSpawning: boolean; isProjectProvisioning: boolean; isProjectRestarting: boolean; isRemoving: boolean; projectDragInProgress: boolean; openOrchestrator: () => Promise<void>; removeProject: () => void }) {
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	const requestLane = useUiStore((state) => state.requestLaneOrchestrator);
	return (
		<>
		{!isStandalone && <div
				className={cn(
					"sidebar-expanded-chrome z-chrome",
					ROW_ACTIONS_CLASS,
					"group-data-[collapsible=icon]:hidden",
					projectDragInProgress && "pointer-events-none",
				)}
				data-project-actions=""
				draggable={false}
				onClick={(event) => event.stopPropagation()}
				onPointerDown={(event) => event.stopPropagation()}
			>
				<Tooltip>
					<TooltipTrigger asChild>
						<span className="inline-flex">
							<button
								aria-current={orchestratorActive ? "page" : undefined}
								aria-label={
									orchestrator
										? t("shell.openProjectOrchestrator", {
										name: nameWithHost,
											})
										: t("shell.spawnProjectOrchestrator", {
										name: nameWithHost,
											})
								}
									className={cn(HOVER_ACTION_CLASS, orchestratorActive && "text-foreground")}
									disabled={isSpawning || isProjectProvisioning || isProjectRestarting}
								onClick={() => void openOrchestrator()}
								type="button"
							>
								<OrchestratorIcon aria-hidden="true" strokeWidth={orchestratorActive ? 2.5 : 2} />
							</button>
						</span>
					</TooltipTrigger>
						<TooltipContent>
							{isProjectProvisioning || isProjectRestarting
								? t("shell.restarting")
								: isSpawning
								? t("shell.spawning")
								: orchestrator
									? t("shell.orchestrator")
									: t("shell.spawnOrchestratorLower")}
					</TooltipContent>
				</Tooltip>
				<DropdownMenu>
					<DropdownMenuTrigger asChild>
						<button
							aria-label={t("shell.projectActions", {
										name: nameWithHost,
							})}
							className={HOVER_ACTION_CLASS}
							type="button"
						>
							<MoreVertical aria-hidden="true" />
						</button>
					</DropdownMenuTrigger>
					<DropdownMenuContent side="right" align="start" className="min-w-44">
						<DropdownMenuItem disabled={isProjectRestarting} onSelect={() => requestNewTask(workspace.id, workspace.hostId)}>
							<Plus aria-hidden="true" />
							{t("shell.newTask")}
						</DropdownMenuItem>
						{!isStandalone ? (
							<DropdownMenuItem disabled={isProjectRestarting} onSelect={() => requestLane(workspace.id)}>
								<GitBranch aria-hidden="true" />
								{t("shell.newLane")}
							</DropdownMenuItem>
						) : null}
		<DropdownMenuItem onSelect={() => selection.goSettings(workspace.id)}>
							<Settings aria-hidden="true" />
							{t("shell.projectSettings")}
						</DropdownMenuItem>
						<DropdownMenuItem
							className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
							disabled={isRemoving}
							onSelect={() => void removeProject()}
						>
							<Trash2 aria-hidden="true" />
							{t("shell.removeProjectTitle")}
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
			</div>}
		</>
	);
}
