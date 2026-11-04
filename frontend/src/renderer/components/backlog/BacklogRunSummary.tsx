import { useTranslation } from "react-i18next";
import type { BacklogRun } from "../../lib/backlog-run-api";
import { BacklogRunUsage } from "./BacklogRunUsage";

/** The end of a run: what landed, what waits on the owner and what is held (counted by code), the orchestrator's narrative, and the totals. */
export function BacklogRunSummary({ run }: { run: BacklogRun }) {
	const { t } = useTranslation();
	const counts = run.counts ?? {};
	const waiting = (counts.needs_you ?? 0) + (counts.failed ?? 0);
	return (
		<div className="space-y-1.5 border-t border-border-strong px-3 py-2">
			<p className="flex flex-wrap items-center gap-x-3 text-sm">
				<span>{t("backlog.run.summary.counts", { landed: counts.landed ?? 0, waiting, held: counts.held ?? 0 })}</span>
				<BacklogRunUsage usage={run.usage} />
			</p>
			{run.summary ? <p className="whitespace-pre-wrap text-sm text-muted-foreground">{run.summary}</p> : null}
		</div>
	);
}
