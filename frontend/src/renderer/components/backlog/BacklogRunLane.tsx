import { useTranslation } from "react-i18next";
import type { BacklogRunItem } from "../../lib/backlog-run-api";
import { BacklogRunItemRow } from "./BacklogRunItemRow";

/** One lane: its items in the order they run. Lanes run in parallel. */
export function BacklogRunLane({ index, items, showTitle, onSelect }: { index: number; items: readonly BacklogRunItem[]; showTitle: boolean; onSelect?: (itemId: string) => void }) {
	const { t } = useTranslation();
	return (
		<section aria-label={t("backlog.run.lane", { n: index + 1 })}>
			{showTitle ? <h3 className="px-3 pt-2 text-xs font-medium text-muted-foreground">{t("backlog.run.lane", { n: index + 1 })}</h3> : null}
			<ul className="divide-y divide-border/60">
				{items.map((item) => <BacklogRunItemRow key={item.itemId} item={item} onSelect={onSelect} />)}
			</ul>
		</section>
	);
}
