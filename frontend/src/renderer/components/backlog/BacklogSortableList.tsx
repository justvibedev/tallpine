import { closestCenter, DndContext, KeyboardSensor, PointerSensor, useSensor, useSensors, type DragEndEvent } from "@dnd-kit/core";
import { SortableContext, verticalListSortingStrategy } from "@dnd-kit/sortable";
import type { BacklogItem } from "../../lib/backlog-api";
import { reorderTarget, type BacklogReorderTarget } from "../../lib/backlog-reorder";

type BacklogSortableListProps = {
	siblings: readonly BacklogItem[];
	onReorder: (item: BacklogItem, target: BacklogReorderTarget) => void;
	children: React.ReactNode;
	className?: string;
};

/** Drag order within one set of siblings: top-level Open items, or the children of one parent. */
export function BacklogSortableList({ siblings, onReorder, children, className }: BacklogSortableListProps) {
	// A small distance keeps a plain click on the handle from starting a drag.
	const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }), useSensor(KeyboardSensor));
	const onDragEnd = ({ active, over }: DragEndEvent) => {
		if (!over || active.id === over.id) return;
		const from = siblings.findIndex((item) => item.id === active.id);
		const to = siblings.findIndex((item) => item.id === over.id);
		const target = reorderTarget(siblings, from, to);
		if (target) onReorder(siblings[from], target);
	};
	return (
		<DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
			<SortableContext items={siblings.map((item) => item.id)} strategy={verticalListSortingStrategy}>
				<ul role="list" className={className}>{children}</ul>
			</SortableContext>
		</DndContext>
	);
}
