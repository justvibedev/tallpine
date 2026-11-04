import { PanelLeft, PanelRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "./ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

/** A panel's open/close control. On the right it leads the tab strip and, collapsed, sits where the panel was; on the left it leads the Sessions panel header. */
export function PanelToggleButton({
	open,
	onClick,
	testId,
	side = "right",
}: {
	open: boolean;
	onClick: () => void;
	testId?: string;
	side?: "left" | "right";
}) {
	const { t } = useTranslation();
	const label = side === "left" ? t("panel.toggleSessions") : t("panel.toggleSidebar");
	const Icon = side === "left" ? PanelLeft : PanelRight;
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<Button type="button" variant="ghost" size="icon-sm" aria-label={label} aria-pressed={open} data-testid={testId} onClick={onClick}>
					<Icon aria-hidden="true" className="size-icon-md" />
				</Button>
			</TooltipTrigger>
			<TooltipContent side="bottom">{label}</TooltipContent>
		</Tooltip>
	);
}
