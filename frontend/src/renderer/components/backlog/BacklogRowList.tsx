import type { BacklogItem } from "../../lib/backlog-api";
import type { BacklogGroups } from "../../lib/backlog-groups";
import type { BacklogReorderTarget } from "../../lib/backlog-reorder";
import { BacklogRow } from "./BacklogRow";
import { BacklogSortableList } from "./BacklogSortableList";
import { BacklogSortableRow } from "./BacklogSortableRow";

export type BacklogRowListProps = {
	items: readonly BacklogItem[];
	groups: Pick<BacklogGroups, "childrenByParent" | "progress">;
	selectedId: string | undefined;
	tabbableId: string | undefined;
	/** Drag order is per project, so it is off while several projects are listed. */
	sortable: boolean;
	muted?: boolean;
	projectNames?: ReadonlyMap<string, string>;
	onSelect: (item: BacklogItem) => void;
	onReorder: (item: BacklogItem, target: BacklogReorderTarget) => void;
};

const LIST_CLASS = "divide-y divide-border/40";

/** A group's rows, each parent followed by its children one level in. */
export function BacklogRowList({ items, groups, selectedId, tabbableId, sortable, muted, projectNames, onSelect, onReorder }: BacklogRowListProps) {
	const rowProps = (item: BacklogItem, nested: boolean) => ({
		item,
		nested,
		muted,
		selected: item.id === selectedId,
		tabbable: item.id === tabbableId,
		progress: groups.progress.get(item.id),
		projectName: projectNames?.get(item.projectId),
		onSelect: () => onSelect(item),
	});
	const childList = (parent: BacklogItem) => {
		const children = groups.childrenByParent.get(parent.id) ?? [];
		if (children.length === 0) return null;
		return sortable ? (
			<BacklogSortableList siblings={children} onReorder={onReorder} className={LIST_CLASS}>
				{children.map((child) => <BacklogSortableRow key={child.id} {...rowProps(child, true)} />)}
			</BacklogSortableList>
		) : (
			<ul role="list" className={LIST_CLASS}>
				{children.map((child) => <li key={child.id}><BacklogRow {...rowProps(child, true)} /></li>)}
			</ul>
		);
	};
	if (!sortable) {
		return (
			<ul role="list" className={LIST_CLASS}>
				{items.map((item) => <li key={item.id}><BacklogRow {...rowProps(item, false)} />{childList(item)}</li>)}
			</ul>
		);
	}
	return (
		<BacklogSortableList siblings={items} onReorder={onReorder} className={LIST_CLASS}>
			{items.map((item) => <BacklogSortableRow key={item.id} {...rowProps(item, false)}>{childList(item)}</BacklogSortableRow>)}
		</BacklogSortableList>
	);
}
