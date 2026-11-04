import { useTranslation } from "react-i18next";
import type { BacklogItem } from "../../lib/backlog-api";
import type { BacklogGroups } from "../../lib/backlog-groups";
import { stepTarget, type BacklogReorderTarget } from "../../lib/backlog-reorder";
import { LiveStatus } from "../LiveStatus";
import { useState } from "react";
import type { BacklogFilter } from "./BacklogToolbar";
import { BacklogEmptyState } from "./BacklogEmptyState";
import { BacklogGroup } from "./BacklogGroup";
import { BacklogStaleRow } from "./BacklogStaleRow";

type BacklogListProps = {
	groups: BacklogGroups;
	filter: BacklogFilter;
	selectedId: string | undefined;
	sortable: boolean;
	projectNames?: ReadonlyMap<string, string>;
	onSelect: (item: BacklogItem | undefined) => void;
	onReorder: (item: BacklogItem, target: BacklogReorderTarget) => void;
};

/** The groups for the active filter, with row-to-row keyboard movement (arrows or j/k) and Alt+arrow reorder. */
export function BacklogList({ groups, filter, selectedId, sortable, projectNames, onSelect, onReorder }: BacklogListProps) {
	const { t } = useTranslation();
	const [announcement, setAnnouncement] = useState("");
	const show = (group: "inbox" | "open" | "held" | "stale" | "archive") =>
		filter === "all" ? group !== "archive" : filter === group || (filter === "open" && group === "stale");
	const visible: BacklogItem[] = [
		...(show("inbox") ? groups.inbox : []),
		...(show("open") ? groups.open : []),
		...(show("held") ? groups.held : []),
		...(show("stale") ? groups.stale : []),
		...(show("archive") ? groups.archive : []),
	];
	if (visible.length === 0) return <BacklogEmptyState kind={filter === "archive" ? "archive" : "filtered"} />;

	const common = { groups, selectedId, tabbableId: selectedId ?? visible[0]?.id, projectNames, onSelect, onReorder };
	const siblingsOf = (item: BacklogItem): readonly BacklogItem[] =>
		item.parentId ? groups.childrenByParent.get(item.parentId) ?? [] : groups.open;

	const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
		const row = (event.target as HTMLElement).closest<HTMLElement>("[data-backlog-row]");
		if (!row) return;
		const direction = event.key === "ArrowDown" || event.key === "j" ? 1 : event.key === "ArrowUp" || event.key === "k" ? -1 : 0;
		if (direction !== 0 && event.altKey && sortable && event.key.startsWith("Arrow")) {
			const item = visible.concat([...groups.childrenByParent.values()].flat()).find((candidate) => candidate.ref === row.dataset.backlogRow && candidate.projectId === row.dataset.projectId);
			const target = item && stepTarget(siblingsOf(item), item.ref, direction === 1 ? "down" : "up");
			if (item && target) {
				event.preventDefault();
				onReorder(item, target);
				const siblings = siblingsOf(item);
				const from = siblings.findIndex((candidate) => candidate.id === item.id);
				setAnnouncement(t("backlog.row.moved", { ref: item.ref, position: from + direction + 1 }));
			}
			return;
		}
		if (direction !== 0 && !event.altKey && !event.metaKey && !event.ctrlKey) {
			const rows = [...event.currentTarget.querySelectorAll<HTMLElement>("[data-backlog-row]")];
			const next = rows[rows.indexOf(row) + direction];
			if (next) {
				event.preventDefault();
				next.focus();
			}
		} else if (event.key === "Escape" && selectedId) {
			// Step back one level: close the detail; a second Esc leaves the page.
			event.preventDefault();
			onSelect(undefined);
		}
	};

	return (
		// eslint-disable-next-line jsx-a11y/no-static-element-interactions -- arrow-key movement between the rows' own buttons
		<div onKeyDown={onKeyDown}>
			<LiveStatus text={announcement} />
			{show("inbox") ? <BacklogGroup {...common} heading={t("backlog.group.inbox")} items={groups.inbox} sortable={false} /> : null}
			{show("open") ? <BacklogGroup {...common} heading={t("backlog.group.open")} items={groups.open} sortable={sortable} /> : null}
			{show("held") ? <BacklogGroup {...common} heading={t("backlog.group.held")} items={groups.held} sortable={false} /> : null}
			{show("stale") ? <BacklogStaleRow {...common} items={groups.stale} sortable={false} /> : null}
			{show("archive") ? <BacklogGroup {...common} heading={t("backlog.group.archive")} items={groups.archive} sortable={false} muted /> : null}
		</div>
	);
}
