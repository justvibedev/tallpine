import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { BACKLOG_STALE_DAYS } from "../../lib/backlog-groups";
import { cn } from "../../lib/utils";
import type { BacklogRowListProps } from "./BacklogRowList";
import { BacklogRowList } from "./BacklogRowList";

/** Items nobody has touched in a long while: one muted line that expands in place. */
export function BacklogStaleRow(list: BacklogRowListProps) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	if (list.items.length === 0) return null;
	return (
		<section className="pb-3">
			<button
				type="button"
				aria-expanded={open}
				onClick={() => setOpen((value) => !value)}
				className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/60"
			>
				<ChevronRight aria-hidden="true" className={cn("size-3 transition-transform motion-reduce:transition-none", open && "rotate-90")} />
				{t("backlog.stale", { count: list.items.length, days: BACKLOG_STALE_DAYS })}
			</button>
			{open ? <BacklogRowList {...list} muted sortable={false} /> : null}
		</section>
	);
}
