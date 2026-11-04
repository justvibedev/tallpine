/** Inspector view and tab types plus shared class names. */
import { type ReactNode } from "react";

export type InspectorView = "summary" | "reviews" | "browser" | "sideChat" | "files" | "artifacts" | "plan";

export type InspectorTab = {
	badge?: boolean;
	/** Corner badge count, e.g. changed files; hidden at zero. */
	count?: number;
	displayLabel?: string;
	icon: ReactNode;
	id: InspectorView;
	label: string;
};

export const inspectorShellClass = "@container/inspector flex h-full min-h-0 flex-col overflow-hidden";
export const inspectorBodyBaseClass = "min-h-0 flex-1";
export const inspectorScrollableBodyClass = "board-scrollbar overflow-x-hidden overflow-y-auto pt-0.5 pb-3";
export const inspectorEmptyClass = "text-xs text-settings-muted leading-normal";
