import { useSortable } from "@dnd-kit/sortable";
import { useCallback, memo, useMemo } from "react";
import { type WorkspaceSession } from "../../types/workspace";
import { SessionRow } from "./SessionRow";

// A session row inside its project's drag context. The Pinned section renders
// plain SessionRows instead: that list is ordered by pin time, not by hand.
export const SortableSessionRow = memo(function SortableSessionRow({
	session,
	active,
	consumeDragClick,
	indented = true,
	layoutDependency,
	listIsDragging,
	dropTransitionDisabled,
	onKilled,
	onOpen,
}: {
	session: WorkspaceSession;
	active: boolean;
	consumeDragClick: (id: string) => boolean;
	indented?: boolean;
	layoutDependency?: string;
	listIsDragging: boolean;
	dropTransitionDisabled: boolean;
	onKilled?: (session: WorkspaceSession) => void;
	onOpen: (sessionId: string) => void;
}) {
	const { isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition } = useSortable({
		id: session.id,
	});
	// dnd-kit's context changes on unrelated list renders and re-renders this
	// wrapper. Keep the row's props stable so the heavy SessionRow skips it.
	const open = useCallback(() => {
		if (!consumeDragClick(session.id)) onOpen(session.id);
	}, [consumeDragClick, onOpen, session.id]);
	const reorder = useMemo(
		() => ({ isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition, dropTransitionDisabled }),
		[isDragging, listeners, setActivatorNodeRef, setNodeRef, transform, transition, dropTransitionDisabled],
	);
	return (
		<SessionRow
			session={session}
			active={active}
			indented={indented}
			onKilled={onKilled}
			onOpen={open}
			layoutDependency={layoutDependency}
			listIsDragging={listIsDragging}
			reorder={reorder}
		/>
	);
});
