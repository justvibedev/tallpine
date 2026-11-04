import { TriangleAlert } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useSilentFailures } from "../hooks/useSilentFailures";
import { cn } from "../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "./NavRowHighlight";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type HealthBadgeProps = { className?: string; tabIndex?: number };

/**
 * Shows only while something is silently stuck: a Chat agent that is not
 * attached, worker reports that are not being delivered, or final-result
 * notices past due. Healthy (or unknown) renders nothing.
 */
export function HealthBadge({ className, tabIndex }: HealthBadgeProps) {
	const { t } = useTranslation();
	const { data } = useSilentFailures();
	if (!data || data.healthy) return null;
	const lines: string[] = [];
	const shown = data.chatNotAttached.slice(0, 5).map((c) => c.sessionId);
	const extra = data.chatNotAttached.length - shown.length;
	const chatIds = extra > 0 ? `${shown.join(", ")} +${extra}` : shown.join(", ");
	if (data.chatNotAttached.length > 0) {
		lines.push(t("health.chatNotAttached", "{{count}} chat session(s) have no attached agent. Send a message to resume, or run `tallpine doctor`.", { count: data.chatNotAttached.length }));
		lines.push(t("health.chatNotAttachedIds", "Sessions: {{ids}}", { ids: chatIds }));
	}
	if (data.reportBatches.length > 0) {
		lines.push(t("health.reportBatches", "Worker reports are not being delivered for {{count}} project(s). Start an orchestrator, or run `tallpine doctor`.", { count: data.reportBatches.length }));
	}
	if (data.turnFallbacks.length > 0) {
		lines.push(t("health.turnFallbacks", "{{count}} final-result notice(s) are overdue. Run `tallpine doctor`.", { count: data.turnFallbacks.length }));
	}
	const title = t("health.title", "Needs attention");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button
					variant="ghost"
					size="icon-sm"
					type="button"
					tabIndex={tabIndex}
					aria-label={title}
					data-testid="health-badge"
					className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "size-9 rounded-lg border-0 text-status-in-review focus-visible:ring-2 focus-visible:ring-ring", className)}
				>
					<NavRowHighlight />
					<span className="relative z-[1] grid place-items-center">
						<TriangleAlert className="size-icon-md" aria-hidden="true" />
					</span>
				</Button>
			</TooltipTrigger>
			<TooltipContent className="max-w-72" data-testid="health-badge-tooltip">
				{lines.map((line) => <p key={line}>{line}</p>)}
			</TooltipContent>
		</Tooltip>
	);
}
