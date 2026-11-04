import { useTranslation } from "react-i18next";
import type { BacklogRunControl, BacklogRunStatus } from "../../lib/backlog-run-api";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

type BacklogRunControlsProps = {
	status: BacklogRunStatus;
	busy: boolean;
	onControl: (action: BacklogRunControl) => void;
};

/** Pause or Resume, and Stop, while the run is open. Each click is one request and one nudge to the orchestrator. */
export function BacklogRunControls({ status, busy, onControl }: BacklogRunControlsProps) {
	const { t } = useTranslation();
	if (status === "done" || status === "stopping") return null;
	return (
		<div className="flex items-center gap-1">
			{status === "paused" ? (
				<Button type="button" size="sm" variant="secondary" disabled={busy} onClick={() => onControl("resume")}>{t("backlog.run.resume")}</Button>
			) : status === "running" ? (
				<Button type="button" size="sm" variant="secondary" disabled={busy} onClick={() => onControl("pause")}>{t("backlog.run.pause")}</Button>
			) : null}
			<Tooltip>
				<TooltipTrigger asChild>
					<Button type="button" size="sm" variant="ghost" className="text-destructive hover:text-destructive" disabled={busy} onClick={() => onControl("stop")}>{t("backlog.run.stop")}</Button>
				</TooltipTrigger>
				<TooltipContent>{t("backlog.run.stopTooltip")}</TooltipContent>
			</Tooltip>
		</div>
	);
}
