import { DndContext, closestCenter, type DragEndEvent } from "@dnd-kit/core";
import { SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { useCallback, useEffect, useMemo, useState } from "react";
import { flushSync } from "react-dom";
import { type WorkspaceSession } from "../../types/workspace";
import { SidebarMenuSub } from "../ui/sidebar";
import { SessionRow } from "./SessionRow";
import { SortableSessionRow } from "./SortableSessionRow";
import { useReorderSensors } from "./reorder-dnd";
import { reorderById, restrictToListBounds } from "./sortable-helpers";
import { usePostDragClickGuard } from "./usePostDragClickGuard";

/** The reorderable session list shared by a project's tree and the Scratchpad
 *  section. It owns the drag context; the committed order stays with the caller
 *  so each list keeps its own persistence and slicing rules. */
export function SessionReorderList({
	dndId,
	testId,
	className,
	sessions,
	sessionIds,
	activeSessionId,
	indented = true,
	plain = false,
	onReorder,
	onKilled,
	onOpen,
}: {
	dndId: string;
	testId: string;
	className: string;
	sessions: WorkspaceSession[];
	sessionIds: string[];
	activeSessionId?: string;
	indented?: boolean;
	/** While a project is being dragged, leave the session lists as plain rows:
	 *  otherwise every expanded project's DnD context measures its sortable
	 *  descendants on drop. */
	plain?: boolean;
	onReorder: (next: string[] | null) => void;
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: (sessionId: string) => void;
}) {
	// Suppress the vertical glide for the first 500ms after the list mounts, so the
	// re-sorts that follow a daemon settle or a project expanding do not shuffle rows.
	const [layoutSettled, setLayoutSettled] = useState(false);
	useEffect(() => {
		const timer = window.setTimeout(() => setLayoutSettled(true), 500);
		return () => window.clearTimeout(timer);
	}, []);
	const orderKey = useMemo(() => sessionIds.join("\u0000"), [sessionIds]);
	const layoutDependency = layoutSettled ? orderKey : undefined;
	const sensors = useReorderSensors();
	const dragClickGuard = usePostDragClickGuard();
	const [listDragging, setListDragging] = useState(false);
	const [dropTransitionDisabledId, setDropTransitionDisabledId] = useState<string | null>(null);

	const onDragEnd = useCallback(({ active, over }: DragEndEvent) => {
		const sessionId = String(active.id);
		dragClickGuard.markDragEnded(sessionId);
		if (!over) {
			setListDragging(false);
			setDropTransitionDisabledId(null);
			return;
		}
		// reorderById rejects any id that is not in THIS list, so a stray
		// cross-list drop leaves both lists' orders untouched.
		const next = reorderById(sessionIds, sessionId, String(over.id));
		// Commit the destination DOM order before dnd-kit removes its live transform.
		// Otherwise the row briefly snaps back to its derived (usually top) position,
		// then Motion animates it forward to the persisted destination.
		flushSync(() => {
			onReorder(next);
			setListDragging(false);
			setDropTransitionDisabledId(sessionId);
		});
		requestAnimationFrame(() => setDropTransitionDisabledId(null));
	}, [dragClickGuard, onReorder, sessionIds]);

	const onDragCancel = useCallback(() => {
		setListDragging(false);
		setDropTransitionDisabledId(null);
	}, []);

	if (plain) {
		return (
			<SidebarMenuSub className={className} data-testid={testId}>
				{sessions.map((session) => (
					<SessionRow
						key={session.id}
						session={session}
						active={activeSessionId === session.id}
						indented={indented}
						onKilled={onKilled}
						onOpen={() => onOpen(session.id)}
					/>
				))}
			</SidebarMenuSub>
		);
	}

	return (
		<DndContext
			collisionDetection={closestCenter}
			modifiers={[restrictToListBounds]}
			id={dndId}
			onDragStart={() => setListDragging(true)}
			onDragCancel={onDragCancel}
			onDragEnd={onDragEnd}
			sensors={sensors}
		>
			<SortableContext items={sessionIds} strategy={verticalListSortingStrategy}>
				<SidebarMenuSub className={className} data-testid={testId}>
					{sessions.map((session) => (
						<SortableSessionRow
							key={session.id}
							session={session}
							active={activeSessionId === session.id}
							consumeDragClick={dragClickGuard.consumeClick}
							indented={indented}
							layoutDependency={layoutDependency}
							listIsDragging={listDragging}
							dropTransitionDisabled={dropTransitionDisabledId === session.id}
							onKilled={onKilled}
							onOpen={onOpen}
						/>
					))}
				</SidebarMenuSub>
			</SortableContext>
		</DndContext>
	);
}
