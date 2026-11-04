import type { MessageKey } from "../../i18n/messages";
import { GripVertical, Lock } from "lucide-react";
import type { useSortable } from "@dnd-kit/sortable";
import { useTranslation } from "react-i18next";
import type { BacklogItem } from "../../lib/backlog-api";
import type { BacklogProgress } from "../../lib/backlog-groups";
import { formatBacklogAge } from "../../lib/backlog-age";
import { cn } from "../../lib/utils";
import { BacklogBadges } from "./BacklogBadges";

export type BacklogRowDrag = Pick<ReturnType<typeof useSortable>, "attributes" | "listeners">;

type BacklogRowProps = {
	item: BacklogItem;
	selected: boolean;
	/** The one row in the list that takes Tab focus. */
	tabbable: boolean;
	nested?: boolean;
	muted?: boolean;
	progress?: BacklogProgress;
	projectName?: string;
	drag?: BacklogRowDrag;
	onSelect: () => void;
};

/** One item: ref, title, then a quiet trailing slot. No border or card; the list's dividers do the separating. */
export function BacklogRow({ item, selected, tabbable, nested = false, muted = false, progress, projectName, drag, onSelect }: BacklogRowProps) {
	const { t, i18n } = useTranslation();
	const age = formatBacklogAge(item.updatedAt, i18n.language);
	const blockers = item.linkCounts?.blocked_by ?? 0;
	const label = t("backlog.row.label", { ref: item.ref, title: item.title, state: t(`backlog.state.${item.state}` as MessageKey), age });
	return (
		<div
			className={cn(
				"group/row flex min-h-8 items-center gap-1 pr-2 hover:bg-interactive-hover",
				selected && "bg-interactive-active",
				nested ? "pl-8" : "pl-2",
			)}
		>
			{drag ? (
				<button
					type="button"
					aria-label={t("backlog.row.reorder", { ref: item.ref })}
					className="flex size-5 shrink-0 cursor-grab touch-none items-center justify-center rounded text-muted-foreground opacity-0 focus-visible:opacity-100 group-hover/row:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
					{...drag.attributes}
					{...drag.listeners}
				>
					<GripVertical aria-hidden="true" className="size-3.5" />
				</button>
			) : (
				<span aria-hidden="true" className="size-5 shrink-0" />
			)}
			<button
				type="button"
				data-backlog-row={item.ref}
				data-project-id={item.projectId}
				tabIndex={tabbable ? 0 : -1}
				aria-label={label}
				aria-current={selected ? "true" : undefined}
				onClick={onSelect}
				className="flex min-w-0 flex-1 items-center gap-2 py-1.5 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60"
			>
				<span className="w-9 shrink-0 text-xs tabular-nums text-muted-foreground">{item.ref}</span>
				<span className={cn("min-w-0 flex-1 truncate", muted && "text-muted-foreground", item.state === "done" && "text-muted-foreground line-through", item.state === "dropped" && "text-muted-foreground")}>
					{item.title}
				</span>
				{projectName ? <span className="hidden max-w-28 shrink-0 truncate text-xs text-muted-foreground @md:inline">{projectName}</span> : null}
				{blockers > 0 ? (
					<span className="inline-flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
						<Lock aria-hidden="true" className="size-3" />
						<span className="sr-only @md:not-sr-only">{t("backlog.row.blocked", { n: blockers })}</span>
					</span>
				) : null}
				{progress && progress.total > 0 ? (
					<span className="shrink-0 text-xs tabular-nums text-muted-foreground">{t("backlog.progress", { done: progress.done, total: progress.total })}</span>
				) : null}
				<BacklogBadges badges={item.badges} />
				<span className="hidden w-16 shrink-0 whitespace-nowrap text-right text-xs text-muted-foreground @sm:block">{age}</span>
			</button>
		</div>
	);
}
