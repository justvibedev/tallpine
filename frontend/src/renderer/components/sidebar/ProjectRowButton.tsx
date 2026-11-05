import { useTranslation } from "react-i18next";
import { ChevronRight, Folder, FolderOpen } from "lucide-react";
import { type MouseEvent } from "react";
import { type WorkspaceSummary } from "../../types/workspace";
import { SidebarMenuButton } from "../ui/sidebar";
import { Badge } from "../ui/badge";
import { cn } from "../../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "../NavRowHighlight";
import { NAV_ROW_CLASS, SIDEBAR_ROW_CLASS } from "./constants";
import { useProjectPin } from "./useProjectPin";

export function ProjectRowButton({ workspace, hostLabel, nameWithHost, isStandalone, expanded, dashboardActive, projectActive, projectIsDragging, projectDragInProgress, onProjectClick, onFolderClick }: { workspace: WorkspaceSummary; hostLabel?: string; nameWithHost: string; isStandalone: boolean; expanded: boolean; dashboardActive: boolean; projectActive: boolean; projectIsDragging: boolean; projectDragInProgress: boolean; onProjectClick: () => void; onFolderClick: (event: MouseEvent) => void }) {
	const { t } = useTranslation();
	const { isPinned } = useProjectPin(workspace, isStandalone);
	return (
		<>
								<div>
									{/* project-sidebar__proj-row */}
									<SidebarMenuButton
										aria-label={hostLabel ? t(isStandalone ? "shell.toggleProject" : "shell.openProjectDashboard", { name: nameWithHost }) : undefined}
										aria-current={dashboardActive ? "page" : undefined}
										aria-expanded={expanded}
										isActive={projectActive}
										tooltip={nameWithHost}
										onClick={onProjectClick}
										className={cn(
											NAV_ROW_CLASS,
											NAV_ROW_HIGHLIGHT_HOST_CLASS,
											// Same 32px row, gap, and icon size as Search/Automations and the
											// Projects header so icons and labels share one left edge.
											SIDEBAR_ROW_CLASS,
											!workspace.hostId && "cursor-grab active:cursor-grabbing",
											isPinned ? "pr-sidebar-project-actions-pinned" : "pr-sidebar-project-actions",
											"transition-none",
											projectIsDragging && "!cursor-grabbing",
											projectDragInProgress && "hover:text-muted-foreground active:text-muted-foreground",
											"group-data-[collapsible=icon]:size-control-board! group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:rounded-lg group-data-[collapsible=icon]:p-0! group-data-[collapsible=icon]:font-semibold",
										)}
									>
										<NavRowHighlight active={projectActive} disabled={projectIsDragging} />
										{/* Expanded sidebar: visual folder/chevron icon (decorative — toggle button is a sibling).
			    size-icon-md matches the Projects section row; an 18px centered box was
			    optically indenting these icons relative to the header. */}
										<span
											aria-hidden="true"
											className="relative z-[1] inline-flex size-icon-md shrink-0 translate-y-px items-center justify-center group-data-[collapsible=icon]:hidden"
											data-expanded={expanded ? "" : undefined}
											data-project-folder-visual=""
										>
											{/* 1.2 — contextual icon swap: scale 0.8↔1 (animated); opacity snaps for hide.
											    Hover paint lives in styles.css (fine pointer only). */}
											<span
												className="inline-flex size-icon-md items-center justify-center transition-[scale] duration-normal ease-[var(--ease-out)] motion-reduce:transition-none"
												data-project-folder-icon=""
											>
												{expanded ? <FolderOpen strokeWidth={1.75} /> : <Folder strokeWidth={1.75} />}
											</span>
											<span
												className={cn(
													"absolute inline-flex size-icon-md scale-[0.8] items-center justify-center opacity-0",
													"transition-[scale,rotate] duration-normal ease-[var(--ease-out)]",
													"motion-reduce:transition-none",
													expanded && "rotate-90",
												)}
												data-project-chevron-icon=""
											>
												<ChevronRight strokeWidth={1.75} />
											</span>
										</span>
										{/* Collapsed icon rail: folder icon */}
										<span
											aria-hidden="true"
											className="relative z-[1] hidden size-8 items-center justify-center group-data-[collapsible=icon]:inline-flex"
										>
											{expanded ? <FolderOpen className="size-5" strokeWidth={1.75} /> : <Folder className="size-5" strokeWidth={1.75} />}
										</span>
										<span
											className="sidebar-expanded-chrome relative z-[1] min-w-0 flex-1 translate-y-px truncate group-data-[collapsible=icon]:hidden"
											data-project-label=""
										>
											{workspace.name}
										</span>
										{hostLabel && <Badge variant="outline" className="sidebar-expanded-chrome relative z-[1] h-4 shrink-0 px-1.5 text-2xs group-data-[collapsible=icon]:hidden">{hostLabel}</Badge>}
									</SidebarMenuButton>
									{/* Folder disclosure toggle: sibling of the nav button, absolutely positioned over
		    the icon area so it intercepts clicks there without nesting buttons. */}
									<button
										aria-label={t("shell.toggleProject", {
											name: nameWithHost,
										})}
										aria-expanded={expanded}
										className="absolute inset-y-0 left-0 z-10 w-9 cursor-pointer bg-transparent group-data-[collapsible=icon]:hidden"
										data-project-folder=""
										tabIndex={-1}
										onClick={onFolderClick}
										type="button"
									/>
								</div>
		</>
	);
}
