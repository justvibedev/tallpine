import { PanelRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { TopbarButton } from "./TopbarButton";

/**
 * The session's one sidebar control: it sits at the far right of the top bar and toggles the panel with its last tab.
 * The tooltip carries the shortcut hint (the aria-label stays plain). The notification count lives on the bell in `PanelSwitcher`.
 */
export function SidebarToggleButton({
	open,
	onClick,
	style,
}: {
	open: boolean;
	onClick: () => void;
	style?: React.CSSProperties;
}) {
	const { t } = useTranslation();
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<TopbarButton aria-label={t("panel.toggleSidebar")} aria-pressed={open} data-testid="sidebar-toggle" onClick={onClick} style={style} variant="icon">
					<PanelRight aria-hidden="true" className="size-icon-md" />
				</TopbarButton>
			</TooltipTrigger>
			<TooltipContent side="bottom">{open ? t("shell.closeInspectorTitle") : t("shell.openInspectorTitle")}</TooltipContent>
		</Tooltip>
	);
}
