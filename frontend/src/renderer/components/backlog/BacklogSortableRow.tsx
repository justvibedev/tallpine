import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { BacklogRow } from "./BacklogRow";

type BacklogSortableRowProps = Omit<React.ComponentProps<typeof BacklogRow>, "drag"> & { children?: React.ReactNode };

/** A row the user can drag by its handle; the handle is a real button, so keyboard users have Alt+Up/Down as well. */
export function BacklogSortableRow({ children, ...props }: BacklogSortableRowProps) {
	const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: props.item.id });
	return (
		<li
			ref={setNodeRef}
			style={{ transform: CSS.Translate.toString(transform), transition, zIndex: isDragging ? 1 : undefined }}
			className={isDragging ? "bg-card opacity-90" : undefined}
		>
			<BacklogRow {...props} drag={{ attributes, listeners }} />
			{children}
		</li>
	);
}
