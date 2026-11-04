import { type ReactNode } from "react";
import { SidebarMenuButton } from "../ui/sidebar";
import { cn } from "../../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "../NavRowHighlight";
import { NAV_ROW_CLASS, SIDEBAR_ROW_CLASS } from "./constants";

/** Search + Automations share this one row so their hover/focus/active treatment
 * (NavRowHighlight pill, foreground text, instant) cannot drift apart. */
export function SidebarTopNavRow({
	active = false,
	disabled,
	icon,
	label,
	onClick,
	tooltip,
	trailing,
}: {
	active?: boolean;
	disabled?: boolean;
	icon: ReactNode;
	label: string;
	onClick: () => void;
	tooltip?: string;
	trailing?: string;
}) {
	return (
		<SidebarMenuButton
			aria-label={label}
			className={cn(
				NAV_ROW_CLASS,
				SIDEBAR_ROW_CLASS,
				NAV_ROW_HIGHLIGHT_HOST_CLASS,
				"transition-none",
				"group-data-[collapsible=icon]:size-control-board! group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:rounded-lg group-data-[collapsible=icon]:p-0!",
			)}
			aria-current={active ? "page" : undefined}
			disabled={disabled}
			isActive={active}
			onClick={onClick}
			tooltip={tooltip}
		>
			<NavRowHighlight active={active} />
			<span className="relative z-[1] inline-flex shrink-0 items-center justify-center">{icon}</span>
			<span className="sidebar-expanded-chrome relative z-[1] min-w-0 flex-1 truncate text-left group-data-[collapsible=icon]:hidden">
				{label}
			</span>
			{trailing ? (
				<span className="sidebar-expanded-chrome relative z-[1] ml-auto shrink-0 font-sans text-caption text-passive group-data-[collapsible=icon]:hidden">
					{trailing}
				</span>
			) : null}
		</SidebarMenuButton>
	);
}

/** Settings sections in place of the project tree while the settings page is open. */
