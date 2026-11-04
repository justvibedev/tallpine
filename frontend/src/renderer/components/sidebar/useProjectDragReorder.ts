import { useCallback, useRef, useState, type DragEvent as ReactDragEvent } from "react";
import { useSidebarOrderStore } from "../../stores/sidebar-order-store";
import { EMPTY_DRAG_IMAGE } from "./reorder-dnd";
import { type ProjectDropPlacement, reorderAtProjectBoundary } from "./sortable-helpers";
import { useGrabbingCursor } from "./useGrabbingCursor";
import { usePostDragClickGuard } from "./usePostDragClickGuard";

export function useProjectDragReorder(projectIds: string[]) {
	const setProjectOrder = useSidebarOrderStore((state) => state.setProjectOrder);

	const projectDragClickGuard = usePostDragClickGuard();
	const [draggingProjectId, setDraggingProjectId] = useState<string | null>(null);
	// Keep the active id and drop target in refs so dragover can decide without a
	// full-list re-render while dragging: only the dragged row re-renders (via
	// `isDragged`), and the drop line updates through dedicated React state.
	const draggingProjectIdRef = useRef<string | null>(null);
	const projectDropTargetRef = useRef<{ overId: string; placement: ProjectDropPlacement } | null>(null);
	useGrabbingCursor(draggingProjectId !== null);
	// The drop line is a single element placed at the boundary's gap centre, so
	// "after A" and "before B" resolve to the same spot (no shift across the
	// boundary). Its top animates so the line slides between projects.
	const [dropLine, setDropLine] = useState<{ top: number; visible: boolean }>({ top: 0, visible: false });

	const clearProjectDropIndicator = useCallback(() => {
		projectDropTargetRef.current = null;
		setDropLine((previous) => ({ ...previous, visible: false }));
	}, []);

	const handleProjectDragStart = useCallback((event: ReactDragEvent<HTMLElement>, projectId: string) => {
		if (!projectIds.includes(projectId)) return;
		event.dataTransfer.effectAllowed = "move";
		// Some engines refuse to begin a drag unless the transfer carries data.
		event.dataTransfer.setData("text/plain", projectId);
		if (EMPTY_DRAG_IMAGE) event.dataTransfer.setDragImage(EMPTY_DRAG_IMAGE, 0, 0);
		draggingProjectIdRef.current = projectId;
		projectDropTargetRef.current = null;
		setDraggingProjectId(projectId);
	}, [projectIds]);

	const handleProjectDragEnd = useCallback(() => {
		const projectId = draggingProjectIdRef.current;
		if (projectId) projectDragClickGuard.markDragEnded(projectId);
		draggingProjectIdRef.current = null;
		clearProjectDropIndicator();
		setDraggingProjectId(null);
	}, [clearProjectDropIndicator, projectDragClickGuard]);

	const handleProjectDragOver = useCallback((event: ReactDragEvent<HTMLElement>, overId: string) => {
		const activeId = draggingProjectIdRef.current;
		if (!activeId || activeId === overId) {
			clearProjectDropIndicator();
			return;
		}
		const row = event.currentTarget;
		const rect = row.getBoundingClientRect();
		const placement: ProjectDropPlacement = event.clientY <= rect.top + rect.height / 2 ? "before" : "after";
		if (reorderAtProjectBoundary(projectIds, activeId, overId, placement) === null) {
			clearProjectDropIndicator();
			return;
		}
		// Only invite the drop once this is a real reorder target.
		event.preventDefault();
		event.dataTransfer.dropEffect = "move";
		const target = projectDropTargetRef.current;
		if (target?.overId === overId && target.placement === placement) return;
		projectDropTargetRef.current = { overId, placement };
		const rows = row.parentElement
			? Array.from(row.parentElement.querySelectorAll<HTMLElement>(":scope > [data-project-drop-target]"))
			: [row];
		const index = rows.indexOf(row);
		const insertAt = placement === "before" ? index : index + 1;
		const last = rows[rows.length - 1];
		const top =
			insertAt <= 0
				? rows[0].offsetTop
				: insertAt >= rows.length
					? last.offsetTop + last.offsetHeight
					: (rows[insertAt - 1].offsetTop + rows[insertAt - 1].offsetHeight + rows[insertAt].offsetTop) / 2;
		setDropLine({ top, visible: true });
	}, [clearProjectDropIndicator, projectIds]);

	const handleProjectDrop = useCallback((event: ReactDragEvent<HTMLElement>) => {
		const activeId = draggingProjectIdRef.current;
		const target = projectDropTargetRef.current;
		if (!activeId || !target) return;
		event.preventDefault();
		const next = reorderAtProjectBoundary(projectIds, activeId, target.overId, target.placement);
		if (next) setProjectOrder(next);
		handleProjectDragEnd();
	}, [handleProjectDragEnd, projectIds, setProjectOrder]);

	return {
		projectDragClickGuard,
		draggingProjectId,
		dropLine,
		handleProjectDragStart,
		handleProjectDragEnd,
		handleProjectDragOver,
		handleProjectDrop,
	};
}
