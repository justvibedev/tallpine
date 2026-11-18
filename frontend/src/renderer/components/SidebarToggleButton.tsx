import { PanelRight } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { TopbarButton } from "./TopbarButton";

/**
 * The session's one sidebar control: it sits at the far right of the top bar and toggles the panel with its last tab.
 * The tooltip carries the shortcut hint (the aria-label stays plain). `dot` marks browser content that arrived
 * while the panel was closed (Browser is a panel tab, so the closed panel has nowhere else to show it).
 */
export function SidebarToggleButton({
	dot = false,
	open,
	onClick,
	style,
}: {
	dot?: boolean;
	open: boolean;
	onClick: () => void;
	style?: React.CSSProperties;
}) {
	const { t } = useTranslation();
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<TopbarButton aria-label={t("panel.toggleSidebar")} aria-pressed={open} className="relative" data-testid="sidebar-toggle" onClick={onClick} style={style} variant="icon">
					<PanelRight aria-hidden="true" className="size-icon-md" />
					{/* Same quiet dot the panel switcher drew on its Browser icon. */}
					{dot ? <span aria-hidden="true" className="pointer-events-none absolute right-0.5 top-0.5 size-1.5 rounded-full bg-primary ring-2 ring-background" data-testid="sidebar-toggle-browser-dot" /> : null}
				</TopbarButton>
			</TooltipTrigger>
			<TooltipContent side="bottom">{open ? t("shell.closeInspectorTitle") : t("shell.openInspectorTitle")}</TooltipContent>
		</Tooltip>
	);
}
