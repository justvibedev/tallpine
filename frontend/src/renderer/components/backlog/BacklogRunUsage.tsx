import { useTranslation } from "react-i18next";
import type { BacklogUsage } from "../../lib/backlog-run-api";
import { formatRunCost, formatRunElapsed } from "../../lib/backlog-run-format";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/** Cost and wall-clock elapsed time. Renders nothing when there is neither. */
export function BacklogRunUsage({ usage }: { usage: BacklogUsage }) {
	const { t } = useTranslation();
	const cost = formatRunCost(usage.costNanos, usage.costComplete);
	const elapsed = formatRunElapsed(usage.elapsedSeconds);
	if (!cost && !elapsed) return null;
	return (
		<span className="inline-flex shrink-0 items-center gap-2 text-xs tabular-nums text-muted-foreground">
			{cost ? (
				usage.costComplete ? (
					<span>{t("backlog.run.cost", { value: cost })}</span>
				) : (
					<Tooltip>
						<TooltipTrigger asChild>
							<span tabIndex={0} className="rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60">{t("backlog.run.cost", { value: cost })}</span>
						</TooltipTrigger>
						<TooltipContent>{t("backlog.run.costPartial")}</TooltipContent>
					</Tooltip>
				)
			) : null}
			{elapsed ? <span>{t("backlog.run.elapsed", { value: elapsed })}</span> : null}
		</span>
	);
}
