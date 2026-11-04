import type { BacklogRowListProps } from "./BacklogRowList";
import { BacklogRowList } from "./BacklogRowList";

type BacklogGroupProps = BacklogRowListProps & { heading: string };

/** A quiet sentence-case heading over a shared list; the count is plain text, not a pill. */
export function BacklogGroup({ heading, ...list }: BacklogGroupProps) {
	if (list.items.length === 0) return null;
	return (
		<section aria-label={heading} className="pb-3">
			<h2 className="px-3 pb-1 pt-2 text-xs font-medium text-muted-foreground">
				{heading} <span className="tabular-nums">{list.items.length}</span>
			</h2>
			<BacklogRowList {...list} />
		</section>
	);
}
