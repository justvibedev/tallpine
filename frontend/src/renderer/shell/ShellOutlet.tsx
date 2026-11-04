import { Outlet } from "@tanstack/react-router";
import { type ReactNode } from "react";
import { useRouterState } from "@tanstack/react-router";
import { ViewCloseButton } from "../components/shell-nav/ViewCloseButton";
import { shellNavSection } from "../components/shell-nav/shell-nav-sections";
import { SettingsPane } from "../components/SettingsDialog";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";
import { LocalSessionHost } from "./LocalSessionHost";

/**
 * The routed page stays mounted while settings is open (so a session's scroll,
 * drafts, and terminal survive) and is only hidden behind the settings page.
 */
export function ShellOutlet({ startingChat }: { startingChat: ReactNode }) {
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const railPage = ["sessions", "home"].indexOf(shellNavSection(pathname)) < 0;
	return (
		<>
			<div className={cn("relative flex min-h-0 flex-1 flex-col", settingsOpen && "hidden", railPage && "pr-11")} data-rail-page={railPage || undefined}>
				<div className={startingChat ? "absolute inset-0 invisible pointer-events-none" : "contents"} inert={startingChat ? true : undefined}><Outlet /><LocalSessionHost /></div>
				{startingChat}
				{railPage ? <ViewCloseButton /> : null}
			</div>
			<SettingsPane />
		</>
	);
}
