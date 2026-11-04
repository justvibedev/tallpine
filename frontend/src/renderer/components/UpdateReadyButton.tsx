import { Clock, RefreshCw, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAppUpdateStatus } from "../hooks/useAppUpdates";
import { tallpineBridge } from "../lib/bridge";
import { cn } from "../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/**
 * Sidebar footer: appears only once a downloaded update is staged, and stays
 * quiet otherwise. Clicking restarts into the new version; an ordinary quit
 * does not install it.
 */
export function UpdateReadyButton({ tabIndex }: { tabIndex?: number }) {
	const { t } = useTranslation();
	const status = useAppUpdateStatus();
	if (status?.state !== "downloaded") return null;
	const label = t("appUpdate.restart", "Restart to update");
	const rowClass = cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "h-9 gap-1.5 rounded-lg border-0 px-2 text-caption focus-visible:ring-2 focus-visible:ring-ring");
	if (status.restartWhenIdle) {
		return (
			<div className="flex items-center gap-1" data-testid="update-pending-idle">
				<span className="flex min-w-0 flex-1 items-center gap-1.5 px-2 text-caption text-muted-foreground">
					<Clock className="size-icon-md shrink-0" aria-hidden="true" />
					<span className="truncate">{t("appUpdate.restartWhenIdlePending", "Will restart when agents are idle")}</span>
				</span>
				<Button
					variant="ghost"
					size="sm"
					type="button"
					tabIndex={tabIndex}
					data-testid="update-cancel-idle"
					aria-label={t("appUpdate.cancelRestartWhenIdle", "Cancel restart")}
					onClick={() => void tallpineBridge.appUpdates.cancelInstallWhenIdle()}
					className="h-9 rounded-lg border-0 px-2 focus-visible:ring-2 focus-visible:ring-ring"
				>
					<X className="size-icon-md" aria-hidden="true" />
				</Button>
			</div>
		);
	}
	return (
		<>
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					variant="ghost"
					size="sm"
					type="button"
					tabIndex={tabIndex}
					data-testid="update-ready"
					onClick={() => void tallpineBridge.appUpdates.install()}
					className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "h-9 gap-1.5 rounded-lg border-0 px-2 text-caption focus-visible:ring-2 focus-visible:ring-ring")}
				>
					<NavRowHighlight />
					<span className="relative z-[1] flex items-center gap-1.5">
						<RefreshCw className="size-icon-md" aria-hidden="true" />
						{label}
					</span>
				</Button>
			</TooltipTrigger>
			<TooltipContent className="max-w-72">
				{t("appUpdate.readyTooltip", "Version {{version}} is ready.", { version: status.version ?? "" })}
			</TooltipContent>
		</Tooltip>
		<Button
			variant="ghost"
			size="sm"
			type="button"
			tabIndex={tabIndex}
			data-testid="update-when-idle"
			onClick={() => void tallpineBridge.appUpdates.installWhenIdle()}
			className={rowClass}
		>
			<NavRowHighlight />
			<span className="relative z-[1] flex items-center gap-1.5">
				<Clock className="size-icon-md" aria-hidden="true" />
				{t("appUpdate.restartWhenIdle", "Restart when idle")}
			</span>
		</Button>
		</>
	);
}
