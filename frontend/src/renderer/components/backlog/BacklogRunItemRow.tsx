import type { BacklogRunItem } from "../../lib/backlog-run-api";
import type { MessageKey } from "../../i18n/messages";
import { useTranslation } from "react-i18next";
import { BacklogRunItemStatus } from "./BacklogRunItemStatus";
import { BacklogRunUsage } from "./BacklogRunUsage";

/** One planned item: ref and title first, then what the owner may need to read (the question or the hold reason). */
export function BacklogRunItemRow({ item, onSelect }: { item: BacklogRunItem; onSelect?: (itemId: string) => void }) {
	const { t } = useTranslation();
	const planLabel = item.planStatus ? t(`backlog.run.planStatus.${item.planStatus}` as MessageKey) : "";
	const detail = item.displayStatus === "needs_you" ? item.question : item.displayStatus === "held" || item.displayStatus === "failed" ? item.reason : "";
	return (
		<li className="flex flex-col gap-0.5 px-3 py-1.5">
			<div className="flex items-center gap-2">
				<span className="shrink-0 text-xs tabular-nums text-muted-foreground">{item.ref}</span>
				{onSelect ? (
					<button type="button" onClick={() => onSelect(item.itemId)} className="min-w-0 flex-1 truncate rounded text-left text-sm hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
						{item.title}
					</button>
				) : (
					<span className="min-w-0 flex-1 truncate text-sm">{item.title}</span>
				)}
				<BacklogRunItemStatus status={item.displayStatus} />
			</div>
			<div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 pl-7 text-xs text-muted-foreground">
				{item.model ? <span>{item.model}</span> : null}
				{planLabel ? (
					onSelect ? (
						<button type="button" data-testid="run-item-plan-status" onClick={() => onSelect(item.itemId)} className="rounded underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
							{planLabel}
						</button>
					) : (
						<span data-testid="run-item-plan-status">{planLabel}</span>
					)
				) : null}
				<BacklogRunUsage usage={item.usage} />
			</div>
			{detail ? <p className="pl-7 text-sm text-foreground">{detail}</p> : null}
		</li>
	);
}
