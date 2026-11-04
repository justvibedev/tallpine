import type { MessageKey } from "../../i18n/messages";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useBacklogRunEnabled } from "../../hooks/useBacklogRunEnabled";
import { useControlBacklogRun } from "../../hooks/useControlBacklogRun";
import { useLatestBacklogRun } from "../../hooks/useLatestBacklogRun";
import { MAX_RUN_WORKERS, runLanes } from "../../lib/backlog-run-format";
import { useUiStore } from "../../stores/ui-store";
import { BacklogRunControls } from "./BacklogRunControls";
import { BacklogRunLane } from "./BacklogRunLane";
import { BacklogRunSummary } from "./BacklogRunSummary";

/**
 * The project's latest run: lanes, each item's status, the workers in use and
 * Pause, Resume and Stop. A finished run folds to its summary line. Live: a
 * `backlog_changed` event invalidates the project's backlog keys, which refetches this.
 */
export function BacklogRunCard({ projectId, onSelectItem }: { projectId: string; onSelectItem?: (itemId: string) => void }) {
	const { t } = useTranslation();
	const enabled = useBacklogRunEnabled();
	const run = useLatestBacklogRun(projectId, enabled).data;
	const control = useControlBacklogRun(projectId);
	const showToast = useUiStore((state) => state.showGlobalToast);
	const [folded, setFolded] = useState<Record<string, boolean>>({});
	if (!enabled || !run) return null;

	const done = run.status === "done";
	const expanded = !(folded[run.id] ?? done);
	const lanes = runLanes(run.items);
	const working = run.counts?.active ?? 0;
	const onControl = (action: "pause" | "resume" | "stop") =>
		control.mutate({ runRef: run.ref, action }, {
			onError: (failure) => showToast(t("backlog.run.error", { message: failure instanceof Error ? failure.message : String(failure) }), undefined, "error"),
		});
	const Chevron = expanded ? ChevronDown : ChevronRight;

	return (
		<section aria-label={t("backlog.run.title", { ref: run.ref })} className="m-3 rounded-lg bg-muted/40">
			<header className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-2">
				<button
					type="button"
					aria-expanded={expanded}
					aria-label={t("backlog.run.toggle", { ref: run.ref })}
					onClick={() => setFolded((previous) => ({ ...previous, [run.id]: expanded }))}
					className="inline-flex items-center gap-1 rounded text-sm font-medium focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
				>
					<Chevron aria-hidden="true" className="size-3.5 text-muted-foreground" />
					{t("backlog.run.title", { ref: run.ref })}
				</button>
				<span className="text-xs text-muted-foreground">
					{t(`backlog.run.status.${run.status}` as MessageKey)} · {t(`backlog.scope.${run.scope}` as MessageKey)}
				</span>
				{!done ? <span className="text-xs tabular-nums text-muted-foreground">{t("backlog.run.workers", { used: working, max: MAX_RUN_WORKERS })}</span> : null}
				<span className="ml-auto"><BacklogRunControls status={run.status} busy={control.isPending} onControl={onControl} /></span>
			</header>
			{run.status === "paused" && run.pauseReason ? <p className="px-3 pb-2 text-sm text-status-in-review">{t("backlog.run.pausedReason", { reason: run.pauseReason })}</p> : null}
			{expanded ? (
				<>
					{lanes.length === 0 ? <p className="px-3 pb-3 text-sm text-muted-foreground">{t("backlog.run.noPlan")}</p> : null}
					{lanes.map((items, index) => <BacklogRunLane key={items[0]?.lane ?? index} index={index} items={items} showTitle={lanes.length > 1} onSelect={onSelectItem} />)}
				</>
			) : null}
			{done ? <BacklogRunSummary run={run} /> : null}
		</section>
	);
}
