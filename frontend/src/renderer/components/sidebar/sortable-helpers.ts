import { type Modifier } from "@dnd-kit/core";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { type CSSProperties } from "react";

export type SortableRow = ReturnType<typeof useSortable>;

/** Session sorting stays vertical and never inherits dnd-kit's scale correction. */
export function sortableRowStyle({ transform, transition, isDragging, dropTransitionDisabled }: Pick<SortableRow, "transform" | "transition" | "isDragging"> & { dropTransitionDisabled?: boolean }): CSSProperties {
	return {
		transform: transform ? CSS.Transform.toString({ ...transform, x: 0, scaleX: 1, scaleY: 1 }) : undefined,
		// The active row must clear its drag transform immediately on drop; its
		// siblings retain dnd-kit's smooth displacement while the pointer moves.
		transition: isDragging || dropTransitionDisabled ? "none" : (transition ?? "transform 180ms cubic-bezier(0.22, 1, 0.36, 1)"),
	};
}

// Session drags use their owning list as the visual boundary.
export const restrictToListBounds: Modifier = ({ activeNodeRect, containerNodeRect, transform }) => {
	if (!activeNodeRect || !containerNodeRect) return transform;
	const minY = containerNodeRect.top - activeNodeRect.top;
	const maxY = containerNodeRect.bottom - activeNodeRect.bottom;
	return {
		...transform,
		x: 0,
		y: Math.min(maxY, Math.max(minY, transform.y)),
	};
};

export type ProjectDropPlacement = "before" | "after";

export function reorderAtProjectBoundary(
	ids: string[],
	activeId: string,
	targetId: string,
	placement: ProjectDropPlacement,
): string[] | null {
	if (activeId === targetId || !ids.includes(activeId) || !ids.includes(targetId)) return null;
	const next = ids.filter((id) => id !== activeId);
	const targetIndex = next.indexOf(targetId);
	next.splice(targetIndex + (placement === "after" ? 1 : 0), 0, activeId);
	return next.every((id, index) => id === ids[index]) ? null : next;
}

export function reorderById(ids: string[], activeId: string, overId: string): string[] | null {
	if (activeId === overId) return null;
	const from = ids.indexOf(activeId);
	const to = ids.indexOf(overId);
	if (from < 0 || to < 0) return null;
	const next = [...ids];
	const [moved] = next.splice(from, 1);
	next.splice(to, 0, moved);
	return next;
}
