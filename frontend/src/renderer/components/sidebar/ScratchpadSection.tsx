import { useTranslation } from "react-i18next";
import { Archive, Plus } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { type WorkspaceSession, type WorkspaceSummary, sortedWorkerSessions, resolveNextNavigationAfterSessionKill, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { useLatestCallback, useStableArray } from "../../hooks/useStable";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";
import { useSidebarOrderStore } from "../../stores/sidebar-order-store";
import { applyOrder } from "../../lib/sidebar-order";
import { sessionUiKey } from "../../lib/hosts";
import { AnimatedSectionBody } from "./AnimatedSectionBody";
import { SectionDisclosure } from "./SectionDisclosure";
import { SessionReorderList } from "./SessionReorderList";
import { ShowMoreRow } from "./ShowMoreRow";
import { SidebarSectionScroller } from "./SidebarSectionScroller";
import { ROW_ACTION_BUTTON_CLASS, SECTION_SCROLLER_CLASS, SIDEBAR_INITIAL_SECTION_LIMIT } from "./constants";
import { sessionDndId } from "./reorder-dnd";
import { type Selection } from "./types";
import { useShowMoreCap } from "./useShowMoreCap";

/** Projectless ("ad hoc") agents. Their own section under Projects — same
 *  header chrome, own capped scroller, own Show more — instead of a project row
 *  appended to the project list. */
export function ScratchpadSection({
	workspace,
	selection,
	isCollapsed,
	open,
	onToggle,
}: {
	workspace: WorkspaceSummary;
	selection: Selection;
	isCollapsed: boolean;
	open: boolean;
	onToggle: () => void;
}) {
	const { t } = useTranslation();
	const requestNewTask = useUiStore((state) => state.requestNewTask);
	// Mirrors the project tree: only termination removes an agent from the
	// sidebar, so a completed PR session stays reachable.
	const visibleSessions = useMemo(
		() => sortedWorkerSessions(workspace.sessions).filter((session) => session.isTerminated !== true),
		[workspace.sessions],
	);
	const [sessionOrder, setSessionOrder] = useState<string[]>([]);
	const sessions = useMemo(
		() => applyOrder(visibleSessions, (session) => session.id, sessionOrder, "start"),
		[sessionOrder, visibleSessions],
	);
	// Publish the order shown here so keyboard navigation follows it.
	const scratchpadKey = sessionUiKey(workspace.id, workspace.hostId);
	useEffect(() => {
		const sessionIds = sessions.map((session) => session.id);
		useSidebarOrderStore.getState().setSessionOrder(scratchpadKey, sessionIds);
		return () => useSidebarOrderStore.getState().clearSessionOrder(scratchpadKey);
	}, [scratchpadKey, sessions]);
	const {
		listed: listedSessions,
		hiddenCount: hiddenSessionCount,
		showAll,
		toggleShowAll,
	} = useShowMoreCap(sessions, SIDEBAR_INITIAL_SECTION_LIMIT, selection.activeSessionId, isCollapsed);
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
	const openSession = useLatestCallback((sessionId: string) => {
		selection.goSession(STANDALONE_WORKSPACE_ID, sessionId);
	});
	const handleSessionKilled = useLatestCallback((killedSession: WorkspaceSession) => {
		if (selection.activeSessionId !== killedSession.id) return;
		const nextRoute = resolveNextNavigationAfterSessionKill(workspace, killedSession.id, sessions);
		// An ad hoc agent has no project board to fall back to.
		if (nextRoute.target === "session") {
			selection.goSession(STANDALONE_WORKSPACE_ID, nextRoute.sessionId);
		} else {
			selection.goHome();
		}
	});

	return (
		<div
			className="sidebar-expanded-chrome mb-2 flex min-h-0 flex-col overflow-hidden group-data-[collapsible=icon]:hidden"
			data-scratchpad-section=""
		>
			<SectionDisclosure
				label={workspace.name}
				open={open}
				onToggle={onToggle}
				className="group/scratchpad mt-1"
				trailing={
					<div className="relative inline-flex items-center">
						<span
							className={cn(
								"pointer-events-none absolute right-full top-0 flex h-full origin-center pr-1.5 scale-[0.8] items-center opacity-0",
								"transition-[scale] duration-normal ease-[var(--ease-out)]",
								"motion-reduce:transition-none",
								"group-has-[:focus-visible]/scratchpad:pointer-events-auto group-has-[:focus-visible]/scratchpad:scale-100 group-has-[:focus-visible]/scratchpad:opacity-100",
							)}
							data-scratchpad-archive-action=""
						>
							<Tooltip>
								<TooltipTrigger asChild>
									<span className="inline-flex">
										<button
											aria-label={t("shell.archivedSessions")}
											className={ROW_ACTION_BUTTON_CLASS}
											onClick={(event) => {
												event.stopPropagation();
												selection.goStandaloneBoard();
											}}
											type="button"
										>
											<Archive aria-hidden="true" />
										</button>
									</span>
								</TooltipTrigger>
								<TooltipContent>{t("shell.archivedSessions")}</TooltipContent>
							</Tooltip>
						</span>
						<Tooltip>
							<TooltipTrigger asChild>
								<span className="inline-flex">
									<button
										aria-label={t("shell.openNewAgent")}
										className={ROW_ACTION_BUTTON_CLASS}
										onClick={() => requestNewTask(STANDALONE_WORKSPACE_ID)}
										type="button"
									>
										<Plus aria-hidden="true" />
									</button>
								</span>
							</TooltipTrigger>
							<TooltipContent>{t("shell.openNewAgent")}</TooltipContent>
						</Tooltip>
					</div>
				}
			/>
			<AnimatedSectionBody open={open && listedSessions.length > 0} className="min-h-0 flex-1">
				<SidebarSectionScroller
					className={`${SECTION_SCROLLER_CLASS} min-h-0 flex-1`}
					testId="sidebar-scratchpad-scroller"
					wrapperClassName="flex flex-col flex-1"
				>
					<SessionReorderList
						dndId={sessionDndId(STANDALONE_WORKSPACE_ID)}
						testId={`session-list-${STANDALONE_WORKSPACE_ID}`}
						className="mx-0 ml-0 translate-x-0 gap-0.5 border-l-0 px-0 py-0.5"
						sessions={listedSessions}
						sessionIds={listedSessionIds}
						activeSessionId={selection.activeSessionId}
						indented={false}
						onReorder={commitSessionOrder}
						onKilled={handleSessionKilled}
						onOpen={openSession}
					/>
				</SidebarSectionScroller>
				{!isCollapsed && hiddenSessionCount > 0 ? (
					<ShowMoreRow
						expanded={showAll}
						label={showAll ? t("shell.showLessAgents") : t("shell.showMoreAgents", { count: hiddenSessionCount })}
						onClick={toggleShowAll}
					/>
				) : null}
			</AnimatedSectionBody>
		</div>
	);
}
