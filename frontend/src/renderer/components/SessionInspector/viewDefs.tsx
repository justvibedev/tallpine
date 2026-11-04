import { File, FolderClosed, MessageSquare } from "lucide-react";
import { type CSSProperties, type ReactNode } from "react";
import { isMacPlatform } from "../../lib/platform";
import { type InspectorView } from "@tallpine/product-ui";
import { ClipboardList, SearchCheck } from "lucide-react";
import { type SessionPRSummary } from "../../hooks/useSessionScmSummary";
import type { MessageKey } from "../../i18n";

/** Views the top-bar switcher owns, left to right. */
export const PANEL_SWITCHER_VIEWS: readonly InspectorView[] = ["browser", "plan"];
export const isMac = isMacPlatform();
export const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as CSSProperties) : undefined;

export const VIEW_DEFS: {
	id: InspectorView;
	labelKey: "inspector.summary" | "inspector.reviewTab" | "inspector.browser" | "inspector.sideChat" | "inspector.files" | "inspector.plan" | "artifacts.title";
	icon: ReactNode;
}[] = [
	{
		id: "summary",
		labelKey: "inspector.summary",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<line x1="8" y1="7" x2="20" y2="7" />
				<line x1="8" y1="12" x2="20" y2="12" />
				<line x1="8" y1="17" x2="16" y2="17" />
				<circle cx="4" cy="7" r="1" />
				<circle cx="4" cy="12" r="1" />
				<circle cx="4" cy="17" r="1" />
			</svg>
		),
	},
	{
		id: "reviews",
		labelKey: "inspector.reviewTab",
		icon: <SearchCheck aria-hidden="true" />,
	},
	{
		id: "browser",
		labelKey: "inspector.browser",
		icon: (
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true">
				<circle cx="12" cy="12" r="9" />
				<line x1="3" y1="12" x2="21" y2="12" />
				<path d="M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18" />
			</svg>
		),
	},
	{ id: "sideChat", labelKey: "inspector.sideChat", icon: <MessageSquare aria-hidden="true" /> },
	{
		id: "files",
		labelKey: "inspector.files",
		icon: <FolderClosed aria-hidden="true" data-testid="files-viewer-icon" />,
	},
	{ id: "artifacts", labelKey: "artifacts.title", icon: <File aria-hidden="true" /> },
	{ id: "plan", labelKey: "inspector.plan", icon: <ClipboardList aria-hidden="true" /> },
];

export const prStateLabelKeys: Record<SessionPRSummary["state"], MessageKey> = {
	open: "pr.state.open",
	draft: "pr.state.draft",
	merged: "pr.state.merged",
	closed: "pr.state.closed",
};
