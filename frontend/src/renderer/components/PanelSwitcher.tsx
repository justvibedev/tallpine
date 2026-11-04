import type { CSSProperties, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import type { InspectorView } from "../stores/ui-store";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { TopbarButton } from "./TopbarButton";

export type PanelSwitcherItem = {
	id: InspectorView;
	label: string;
	icon: ReactNode;
	/** Shortcut hint appended to the tooltip. */
	shortcut?: string;
	/** Accessible name when it differs from `label` (the badge itself is aria-hidden, so carry the count here). */
	ariaLabel?: string;
	/** Unread count drawn on the icon, capped at 99+. */
	badge?: number;
	/** A quiet dot for content that arrived without being looked at. */
	dot?: boolean;
};

/**
 * The panel's view switcher, drawn in the top bar left of the sidebar toggle.
 * An icon opens the panel on its view; the active icon of an open panel closes it.
 */
export function PanelSwitcher({
	activeView,
	items,
	onClose,
	onSelect,
	open,
	style,
}: {
	activeView: InspectorView;
	items: readonly PanelSwitcherItem[];
	onClose: () => void;
	onSelect: (view: InspectorView) => void;
	open: boolean;
	style?: CSSProperties;
}) {
	const { t } = useTranslation();
	return (
		<div aria-label={t("panel.switcher")} className="flex items-center gap-1" data-testid="panel-switcher" role="group">
			{items.map((item) => {
				const active = open && activeView === item.id;
				const badgeLabel = item.badge ? (item.badge > 99 ? "99+" : String(item.badge)) : null;
				return (
					<Tooltip key={item.id}>
						<TooltipTrigger asChild>
							<TopbarButton
								aria-label={item.ariaLabel ?? item.label}
								aria-pressed={active}
								className={cn("relative [&_svg]:size-icon-md", active && "bg-interactive-active text-foreground")}
								data-testid={`panel-switcher-${item.id}`}
								onClick={() => (active ? onClose() : onSelect(item.id))}
								style={style}
								variant="icon"
							>
								{item.icon}
								{badgeLabel ? (
									<span
										aria-hidden="true"
										className="pointer-events-none absolute -right-1 -top-1 grid h-3.5 min-w-3.5 place-items-center rounded-full bg-accent-strong px-0.5 font-mono text-[8px] font-semibold leading-none text-accent-foreground ring-1 ring-background"
										data-testid={`panel-switcher-${item.id}-badge`}
									>
										{badgeLabel}
									</span>
								) : item.dot && !active ? (
									<span aria-hidden="true" className="pointer-events-none absolute right-0.5 top-0.5 size-1.5 rounded-full bg-primary ring-2 ring-background" data-testid={`panel-switcher-${item.id}-dot`} />
								) : null}
							</TopbarButton>
						</TooltipTrigger>
						<TooltipContent side="bottom">{item.shortcut ? `${item.label} · ${item.shortcut}` : item.label}</TooltipContent>
					</Tooltip>
				);
			})}
		</div>
	);
}
