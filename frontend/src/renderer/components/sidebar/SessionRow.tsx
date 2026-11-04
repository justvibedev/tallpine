import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Archive, Layers, Pencil } from "lucide-react";
import { useCallback, useEffect, memo, useRef, useState } from "react";
import { motion, useReducedMotion } from "motion/react";
import { type WorkspaceSession } from "../../types/workspace";
import { hasTrustedApiBaseUrl } from "../../lib/api-client";
import { canResumeAgent, resumeAgentOnOpen } from "../../hooks/useCanResumeAgent";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";
import { conversationQueryKey, conversationQueryOptions } from "../../hooks/conversation";
import { OrchestratorIcon } from "../icons";
import { MAX_SESSION_DISPLAY_NAME_LEN, useSessionRename } from "../../hooks/useSessionRename";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "../ui/context-menu";
import { SidebarMenuSubItem } from "../ui/sidebar";
import { Badge } from "../ui/badge";
import { cn } from "../../lib/utils";
import { NavRowHighlight } from "../NavRowHighlight";
import { useTerminateSession } from "../../hooks/useTerminateSession";
import { SessionArchiveDialog, type SessionArchiveChoice } from "../SessionArchiveDialog";
import { EndLaneDialog } from "./EndLaneDialog";
import { SessionCopyContextItems } from "../SessionCopyMenuItems";
import { SessionActions } from "./SessionActions";
import { SessionMessageAge } from "./SessionMessageAge";
import { SessionStatusDot } from "./SessionStatusDot";
import { AGENT_WARM_UP_HOVER_MS } from "./constants";
import { type SortableRow, sortableRowStyle } from "./sortable-helpers";
import { useGrabbingCursor } from "./useGrabbingCursor";

type SessionReorder = Pick<SortableRow, "isDragging" | "listeners" | "setActivatorNodeRef" | "setNodeRef" | "transform" | "transition"> & {
	dropTransitionDisabled: boolean;
};

// One worker-session row. Reads as a link by default; double-click/double-tap
// on the name or F2 flips the label into an inline input (Enter/blur saves,
// Escape cancels) that persists through the daemon rename endpoint.
export const SessionRow = memo(function SessionRow({
	session,
	active,
	hostLabel,
	indented = true,
	layoutDependency,
	listIsDragging = false,
	onKilled,
	onOpen,
	reorder,
}: {
	session: WorkspaceSession;
	active: boolean;
	hostLabel?: string;
	indented?: boolean;
	/** The list order. Present only for rows in a reorderable list: the row then
	 *  animates its vertical position (and nothing else) when the order changes. */
	layoutDependency?: string;
	listIsDragging?: boolean;
	/** Project drags pause nested session projection work. */
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: () => void;
	/** Present only for rows inside a reorderable project list. */
	reorder?: SessionReorder;
}) {
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	useGrabbingCursor(Boolean(reorder?.isDragging));
	const queryClient = useQueryClient();
	const [archiveOpen, setArchiveOpen] = useState(false);
	const [endLaneOpen, setEndLaneOpen] = useState(false);
	const onKilledRef = useRef(onKilled);
	onKilledRef.current = onKilled;
	const { mutate: terminateSession } = useTerminateSession({
		onOptimistic: (killed) => onKilledRef.current?.(killed),
	});
	const refreshWorkspaces = useCallback(
		() => queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(session.hostId) }),
		[queryClient, session.hostId],
	);
	const rename = useSessionRename(session, refreshWorkspaces);
	const lastTouchAtRef = useRef(0);
	const suppressTouchOpenRef = useRef(false);
	const hoverTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	const canPrefetch = session.mode === "chat" && !session.hostId && !active && !listIsDragging && !reorder?.isDragging;
	useEffect(() => () => clearTimeout(hoverTimerRef.current), [canPrefetch]);
	const resumeTimerRef = useRef<ReturnType<typeof setTimeout>>(undefined);
	useEffect(() => () => clearTimeout(resumeTimerRef.current), [canPrefetch]);
	const prefetchConversation = () => {
		if (!canPrefetch || !hasTrustedApiBaseUrl() || queryClient.getQueryData(conversationQueryKey(session.id))) return;
		void queryClient.prefetchInfiniteQuery(conversationQueryOptions(session.id));
	};
	// A stopped agent takes seconds to come back. Start it while the pointer rests
	// on the row so the chat is ready when it opens; opening joins this request.
	const warmUpAgent = () => {
		if (!canPrefetch || !hasTrustedApiBaseUrl() || !canResumeAgent(session)) return;
		void resumeAgentOnOpen(session.id)
			.catch(() => {})
			.finally(() => {
				void refreshWorkspaces();
				void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id) });
			});
	};
	const beginRename = useCallback(() => {
		rename.begin();
	}, [rename.begin]);

	if (rename.isEditing) {
		return (
			<SidebarMenuSubItem className={cn(indented && "pl-0.5")}>
				<div
					className={cn(
						"group/nav-row relative flex h-8 w-full items-center gap-1.5 rounded-lg py-0 pl-1.5 pr-1",
						active && "text-foreground",
					)}
					data-session-row=""
				>
					<NavRowHighlight active={active} />
					<SessionStatusDot session={session} />
					<input
						aria-label={t("shell.renameSession", { title: session.title })}
						autoFocus
						className={cn(
							"relative z-[1] h-full min-w-0 flex-1 appearance-none border-0 bg-transparent! p-0 text-sm text-foreground outline-none ring-0 focus:outline-none focus:ring-0",
							(session.lastInteractionAt ?? session.lastUserMessageAt) && "pr-[36px]",
						)}
						data-session-inline-editor=""
						maxLength={MAX_SESSION_DISPLAY_NAME_LEN}
						onBlur={() => void rename.commit()}
						onChange={(e) => rename.setDraft(e.target.value)}
						onFocus={(e) => e.currentTarget.select()}
						onKeyDown={(e) => {
							if (e.key === "Enter") {
								e.preventDefault();
								e.currentTarget.blur();
							} else if (e.key === "Escape") {
								e.preventDefault();
								rename.cancel();
							}
						}}
						value={rename.draft}
					/>
					<SessionMessageAge session={session} />
				</div>
			</SidebarMenuSubItem>
		);
	}

	return (
		<ContextMenu>
			<ContextMenuTrigger asChild>
				<SidebarMenuSubItem
					className={cn(indented && "pl-0.5", session.lane && "mb-1 border-b border-sidebar-border pb-1 [&:has(+[data-lane-row])]:mb-0 [&:has(+[data-lane-row])]:border-b-0 [&:has(+[data-lane-row])]:pb-0", reorder?.isDragging && "z-chrome cursor-grabbing opacity-60")}
					data-lane-row={session.lane ? "" : undefined}
					data-dragging={reorder?.isDragging ? "true" : undefined}
					ref={reorder?.setNodeRef}
					style={reorder ? sortableRowStyle(reorder) : undefined}
				>
			<motion.div
				layout={layoutDependency !== undefined && !listIsDragging ? "y" : false}
				layoutDependency={layoutDependency}
				transition={prefersReducedMotion ? { duration: 0 } : { layout: { type: "spring", stiffness: 520, damping: 42, mass: 0.55 } }}
			>
				<div
					className={cn(
						"group/session-row group/nav-row relative flex h-8 w-full items-center rounded-lg",
						"hover:text-foreground",
						active && "text-foreground",
					)}
					data-session-row=""
					data-dragging={reorder?.isDragging ? "true" : undefined}
				>
					<NavRowHighlight active={active} disabled={Boolean(reorder?.isDragging)} />
					<div className={cn("relative z-[1] flex min-w-0 flex-1", reorder?.isDragging && "cursor-grabbing")}>
						<button
							aria-current={active ? "page" : undefined}
							data-testid={session.hostId ? "remote-session-row" : undefined}
							aria-keyshortcuts="F2"
							aria-label={t("shell.openSession", { title: hostLabel ? `${session.title} · ${hostLabel}` : session.title })}
							className={cn(
								"flex h-8 min-w-0 flex-1 items-center gap-1.5 rounded-lg py-0 pl-1.5 text-left text-sm outline-hidden focus-visible:ring-2 focus-visible:ring-sidebar-ring",
								(session.lastInteractionAt ?? session.lastUserMessageAt) ? "pr-[36px]" : "pr-2.5",
								!reorder?.isDragging &&
									"group-hover/session-row:pr-sidebar-project-actions group-has-[:focus-visible]/session-row:pr-sidebar-project-actions",
								reorder && "cursor-grab active:cursor-grabbing",
								reorder?.isDragging && "!cursor-grabbing",
							)}
							{...(reorder?.listeners ?? {})}
							onMouseEnter={() => {
								if (!canPrefetch) return;
								hoverTimerRef.current = setTimeout(prefetchConversation, 100);
								resumeTimerRef.current = setTimeout(warmUpAgent, AGENT_WARM_UP_HOVER_MS);
							}}
							onMouseLeave={() => {
								clearTimeout(hoverTimerRef.current);
								clearTimeout(resumeTimerRef.current);
							}}
							onFocus={prefetchConversation}
							onClick={(event) => {
								if (event.detail > 1) return;
								if (suppressTouchOpenRef.current) {
									suppressTouchOpenRef.current = false;
									return;
								}
								onOpen();
							}}
							onKeyDown={(event) => {
								reorder?.listeners?.onKeyDown?.(event);
								if (event.key !== "F2") return;
								event.preventDefault();
								beginRename();
							}}
							onDoubleClick={(event) => {
								event.preventDefault();
								event.stopPropagation();
								beginRename();
							}}
							ref={reorder?.setActivatorNodeRef}
							type="button"
						>
							{session.lane ? (
								<OrchestratorIcon aria-hidden="true" className="relative z-[1] mx-1.5 size-3.5 shrink-0 text-muted-foreground" data-lane-glyph="" />
							) : (
								<SessionStatusDot session={session} />
							)}
							<span className="flex min-w-0 flex-1 items-center gap-1.5">
								<span
									className={cn(
										"min-w-0 flex-1 truncate",
										active ? "text-foreground" : "text-muted-foreground group-hover/session-row:text-foreground",
									)}
									data-session-name=""
									title={session.lane ? `${session.title} (${session.lane})` : undefined}
									onPointerUp={(event) => {
										if (event.pointerType !== "touch") return;
										const now = Date.now();
										if (now - lastTouchAtRef.current <= 500) {
											suppressTouchOpenRef.current = true;
										beginRename();
										}
										lastTouchAtRef.current = now;
									}}
								>
									{session.title}
								</span>
								{hostLabel ? <Badge variant="outline" className="h-4 shrink-0 px-1.5 text-2xs">{hostLabel}</Badge> : null}
							</span>
						</button>
					</div>
					{/* The timestamp is stable at the right edge. Pin and kill use label
					    space while idle, then reveal without changing the row footprint. */}
					<SessionActions
						isDragging={Boolean(reorder?.isDragging)}
						onKilled={onKilled}
						session={session}
					/>
				</div>
			</motion.div>
				</SidebarMenuSubItem>
			</ContextMenuTrigger>
			<ContextMenuContent className="min-w-44">
				<ContextMenuItem aria-label={t("shell.renameSession", { title: session.title })} onSelect={beginRename}>
					<Pencil aria-hidden="true" />
					{t("shell.rename")}
				</ContextMenuItem>
				<ContextMenuItem aria-label={t("shell.archiveSession")} onSelect={() => setArchiveOpen(true)}>
					<Archive aria-hidden="true" />
					{t("shell.archiveSession")}
				</ContextMenuItem>
				<SessionCopyContextItems projectId={session.workspaceId} sessionId={session.id} />
				{session.lane ? (
					<ContextMenuItem onSelect={() => setEndLaneOpen(true)}>
						<Layers aria-hidden="true" />
						{t("shell.endLane")}
					</ContextMenuItem>
				) : null}
			</ContextMenuContent>
			<SessionArchiveDialog
				onConfirm={({ deleteArtifacts }: SessionArchiveChoice) => {
					setArchiveOpen(false);
					terminateSession({ ...session, deleteArtifacts });
				}}
				onOpenChange={setArchiveOpen}
				open={archiveOpen}
				session={session}
				trigger={<></>}
			/>
			{session.lane ? <EndLaneDialog onOpenChange={setEndLaneOpen} open={endLaneOpen} session={session} /> : null}
		</ContextMenu>
	);
});
