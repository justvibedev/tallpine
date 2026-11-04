import { useTranslation } from "react-i18next";
import { type RefObject } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { cn } from "../../lib/utils";
import { SessionReorderList } from "./SessionReorderList";
import { ShowMoreRow } from "./ShowMoreRow";
import { sessionDndId } from "./reorder-dnd";
import { type Selection } from "./types";
import { useProjectSessions } from "./useProjectSessions";

export function ProjectSessionsList({ expanded, hasSessions, projectSessionsRef, animReady, suppressInitialExpandAnimation, hasInteractedWithDisclosure, projectKey, projectDragInProgress, selection, sessionList }: { expanded: boolean; hasSessions: boolean; projectSessionsRef: RefObject<HTMLDivElement | null>; animReady: boolean; suppressInitialExpandAnimation: boolean; hasInteractedWithDisclosure: RefObject<boolean>; projectKey: string; projectDragInProgress: boolean; selection: Selection; sessionList: ReturnType<typeof useProjectSessions> }) {
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	const {
		listedSessions,
		listedSessionIds,
		hiddenSessionCount,
		showAllSessions,
		toggleShowAllSessions,
		commitSessionOrder,
		openSession,
		handleSessionKilled,
	} = sessionList;
	return (
		<AnimatePresence initial={false}>
			{expanded && hasSessions && (
				<motion.div
					key="sessions"
					ref={projectSessionsRef}
					initial={
						animReady && (!suppressInitialExpandAnimation || hasInteractedWithDisclosure.current) ? { gridTemplateRows: "0fr" } : false
					}
					animate={{ gridTemplateRows: "1fr" }}
					exit={{ gridTemplateRows: "0fr" }}
					transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.14, ease: [0.25, 0.46, 0.45, 0.94] }}
					style={{ display: "grid" }}
					className="sidebar-expanded-chrome"
				>
					<div style={{ minHeight: 0, overflow: "hidden" }}>
					<motion.div
						initial={
							animReady && (!suppressInitialExpandAnimation || hasInteractedWithDisclosure.current)
								? { y: -12, opacity: 0 }
								: false
						}
						animate={{ y: 0, opacity: 1 }}
						exit={{ y: -12, opacity: 0 }}
						transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.14, ease: [0.25, 0.46, 0.45, 0.94] }}
					>
											<SessionReorderList
										dndId={sessionDndId(projectKey)}
										testId={`session-list-${projectKey}`}
												className={cn(
													"mx-0 ml-3.5 translate-x-0 gap-px border-l-0 px-0 pt-1",
													hiddenSessionCount > 0 ? "pb-px" : "pb-1",
												)}
												sessions={listedSessions}
												sessionIds={listedSessionIds}
												activeSessionId={selection.activeSessionId}
												plain={projectDragInProgress}
												onReorder={commitSessionOrder}
												onKilled={handleSessionKilled}
												onOpen={openSession}
											/>
											{hiddenSessionCount > 0 ? (
												// Indented to the session list so its label starts on the status-dot column.
												<div className="pl-4">
													<ShowMoreRow
														className="px-3"
														expanded={showAllSessions}
														label={
															showAllSessions
																? t("shell.showLessAgents")
																: t("shell.showMoreAgents", { count: hiddenSessionCount })
														}
														onClick={toggleShowAllSessions}
													/>
												</div>
											) : null}
								</motion.div>
							</div>
							</motion.div>
						)}
					</AnimatePresence>
	);
}
