/** Tab strip pieces: tooltip, sortable top tab and drag overlay. */
import { memo, useEffect, useState, type ReactElement } from "react";
import { useTranslation } from "react-i18next";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { Globe2, X } from "lucide-react";
import { type BrowserViewModel } from "../../hooks/useBrowserView";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";
import { cn } from "../../lib/utils";
import { browserTabLabel } from "../../lib/browser-tab-label";
import { RemoteImg } from "../RemoteImg";

export function BrowserControlTooltip({
	children,
	disabled = false,
	label,
}: {
	children: ReactElement;
	disabled?: boolean;
	label: string;
}) {
	const [open, setOpen] = useState(false);
	useEffect(() => {
		if (disabled) setOpen(false);
	}, [disabled]);
	return (
		<Tooltip open={open} onOpenChange={(nextOpen) => setOpen(disabled ? false : nextOpen)}>
			<TooltipTrigger asChild>{children}</TooltipTrigger>
			<TooltipContent data-browser-native-overlay="true" side="bottom" sideOffset={4}>
				{label}
			</TooltipContent>
		</Tooltip>
	);
}

export const SortableBrowserTopTab = memo(function SortableBrowserTopTab({
	tab,
	selected,
	onSelect,
	onClose,
}: {
	tab: BrowserViewModel["tabs"][number];
	selected: boolean;
	onSelect: (tabId: string) => void;
	onClose: (tabId: string) => void;
}) {
	const { t } = useTranslation();
	const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: tab.id });
	const label = browserTabLabel(tab.title, tab.url);
	const closeLabel = t("browser.closeTab", { title: label.title });
	return (
		<div
			className={cn(
				"browser-panel__tab",
				selected && "browser-panel__tab--active",
				isDragging && "browser-panel__tab--drag-placeholder",
			)}
			data-browser-tab-id={tab.id}
			ref={setNodeRef}
			style={{ transform: CSS.Transform.toString(transform), transition }}
		>
			<BrowserControlTooltip label={label.title}>
				<button
					{...attributes}
					{...listeners}
					aria-selected={selected}
					className="browser-panel__tab-select"
					onClick={() => void onSelect(tab.id)}
					role="tab"
					tabIndex={selected ? 0 : -1}
					type="button"
				>
					{tab.favicon ? (
						<RemoteImg alt="" className="browser-panel__tab-icon object-cover" src={tab.favicon} />
					) : (
						<Globe2 aria-hidden="true" className="browser-panel__tab-icon" />
					)}
					<span className="browser-panel__tab-title">{label.title}</span>
				</button>
			</BrowserControlTooltip>
			<BrowserControlTooltip label={closeLabel}>
				<button
					aria-label={closeLabel}
					className="browser-panel__tab-close"
					onClick={() => onClose(tab.id)}
					type="button"
				>
					<X aria-hidden="true" className="size-icon-base" />
				</button>
			</BrowserControlTooltip>
		</div>
	);
});

export const BrowserTopTabDragOverlay = memo(function BrowserTopTabDragOverlay({
	tab,
}: {
	tab: BrowserViewModel["tabs"][number];
}) {
	const label = browserTabLabel(tab.title, tab.url);
	return (
		<div
			aria-hidden="true"
			className="browser-panel__tab browser-panel__tab--drag-overlay"
			data-testid="browser-tab-drag-overlay"
		>
			<div className="browser-panel__tab-select">
				{tab.favicon ? (
					<RemoteImg alt="" className="browser-panel__tab-icon object-cover" src={tab.favicon} />
				) : (
					<Globe2 aria-hidden="true" className="browser-panel__tab-icon" />
				)}
				<span className="browser-panel__tab-title">{label.title}</span>
			</div>
			<span className="browser-panel__tab-close">
				<X aria-hidden="true" className="size-icon-base" />
			</span>
		</div>
	);
});
