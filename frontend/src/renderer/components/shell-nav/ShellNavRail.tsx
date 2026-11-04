import { useNavigate, useRouterState } from "@tanstack/react-router";
import { CalendarClock, File, Inbox, ListTodo, MessageSquare, PanelLeftClose, PanelLeftOpen, Settings, Smartphone, SquareTerminal } from "lucide-react";
import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { sidebarIsVisible, useUiStore } from "../../stores/ui-store";
import type { WorkspaceSummary } from "../../types/workspace";
import { BuildInfoTooltip } from "../BuildInfoTooltip";
import { TallpineMark } from "../TallpineMark";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS } from "../NavRowHighlight";
import { RailButton } from "./RailButton";
import { shellNavSection } from "./shell-nav-sections";
import { useRailBadges } from "./useRailBadges";
import { useRailPageEscape } from "./useRailPageEscape";

/**
 * The shell's far-left icon rail: home, the main pages, and Settings/Mobile at
 * the bottom. Always visible; the Sessions panel beside it collapses on its own.
 */
export function ShellNavRail({
	workspaces,
	underTopbar,
	topbarOffset,
}: {
	workspaces: WorkspaceSummary[];
	underTopbar: boolean;
	topbarOffset: "toolbar" | "titlebar" | "trafficLights" | "session";
}) {
	const { t } = useTranslation();
	const navigate = useNavigate();
	const pathname = useRouterState({ select: (state) => state.location.pathname });
	const section = shellNavSection(pathname);
	// The last Sessions-section page, so leaving a full-screen view (or the rail tab) lands on the same project/session chat.
	const onSessionsPage = section === "sessions" || section === "home";
	const setLastSessionsPath = useUiStore((state) => state.setLastSessionsPath);
	useEffect(() => {
		if (onSessionsPage && pathname !== "/") setLastSessionsPath(pathname);
	}, [onSessionsPage, pathname, setLastSessionsPath]);
	useRailPageEscape(!onSessionsPage);
	const closeSettings = useUiStore((state) => state.closeSettings);
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	// A rail tab always leaves Settings; re-clicking the active tab returns to the chat.
	const openPage = (to: string, target: typeof section) => {
		if (settingsOpen) closeSettings();
		if (section === target) {
			if (!settingsOpen) void navigate({ to: useUiStore.getState().lastSessionsPath });
			return;
		}
		void navigate({ to });
	};
	const onSessionsClick = () => {
		if (settingsOpen) closeSettings();
		if (onSessionsPage) return settingsOpen ? undefined : toggleSidebar();
		void navigate({ to: useUiStore.getState().lastSessionsPath });
		if (!sidebarIsVisible(useUiStore.getState())) toggleSidebar();
	};
	const panelOpen = useUiStore(sidebarIsVisible);
	const toggleSidebar = useUiStore((state) => state.toggleSidebar);
	const expanded = useUiStore((state) => state.isRailExpanded);
	const toggleRailExpanded = useUiStore((state) => state.toggleRailExpanded);
	const openGlobalSettings = useUiStore((state) => state.openGlobalSettings);
	const badges = useRailBadges(workspaces);
	const attentionLabel = badges.attention > 0 ? t("shellNav.sessionsAttention", { count: badges.attention }) : t("shellNav.sessions");

	return (
		<nav
			aria-label={t("shellNav.label")}
			className={cn(
				"sidebar-focusless fixed bottom-0 left-0 z-chrome hidden w-(--tallpine-rail-w) flex-col gap-1 border-r border-sidebar-border bg-sidebar px-1.5 pb-2 md:flex",
				expanded ? "items-stretch" : "items-center",
				underTopbar ? "top-(--sidebar-chrome-offset)" : "top-0",
			)}
			data-slot="shell-nav-rail"
			data-topbar-offset={underTopbar ? topbarOffset : undefined}
		>
			<BuildInfoTooltip>
				<button
					aria-label={t("shell.goHome")}
					className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "mb-1 mt-2 grid size-control-board place-items-center rounded-lg text-foreground", expanded && "pl-2.5 [justify-items:start]")}
					data-rail-brand=""
					data-testid="rail-home"
					onClick={() => void navigate({ to: "/" })}
					type="button"
				>
					<TallpineMark className="size-6" />
				</button>
			</BuildInfoTooltip>
			<RailButton expanded={expanded}
				active={section === "sessions" || section === "home"}
				icon={<MessageSquare aria-hidden="true" />}
				label={attentionLabel}
				visibleLabel={t("shellNav.sessions")}
				badge={badges.attention}
				badgeTone="attention"
				onClick={onSessionsClick}
				pressed={panelOpen}
				testId="rail-sessions"
			/>
			<RailButton expanded={expanded} active={section === "automations"} icon={<CalendarClock aria-hidden="true" />} label={t("automations.title")} onClick={() => openPage("/automations", "automations")} testId="rail-automations" />
			<RailButton expanded={expanded} active={section === "artifacts"} icon={<File aria-hidden="true" />} label={t("artifacts.title")} badge={badges.artifacts} onClick={() => openPage("/artifacts", "artifacts")} testId="rail-artifacts" />
			<RailButton expanded={expanded} active={section === "backlog"} icon={<ListTodo aria-hidden="true" />} label={t("backlog.title")} onClick={() => openPage("/backlog", "backlog")} testId="rail-backlog" />
			<RailButton expanded={expanded} active={section === "inbox"} icon={<Inbox aria-hidden="true" />} label={t("shellNav.inbox")} badge={badges.inbox} badgeTone="attention" onClick={() => openPage("/inbox", "inbox")} testId="rail-inbox" />
			<RailButton expanded={expanded} active={section === "terminals"} icon={<SquareTerminal aria-hidden="true" />} label={t("shellNav.terminals")} onClick={() => openPage("/terminals", "terminals")} testId="rail-terminals" />
			<div className={cn("mt-auto flex flex-col gap-1", expanded ? "items-stretch" : "items-center")}>
				<RailButton expanded={expanded} ariaExpanded={expanded} icon={expanded ? <PanelLeftClose aria-hidden="true" /> : <PanelLeftOpen aria-hidden="true" />} label={expanded ? t("shellNav.collapse") : t("shellNav.expand")} onClick={toggleRailExpanded} testId="rail-toggle" />
				<RailButton expanded={expanded} icon={<Smartphone aria-hidden="true" />} label={t("settings.connectMobile")} onClick={() => openGlobalSettings("connections")} testId="rail-mobile" />
				<RailButton expanded={expanded} icon={<Settings aria-hidden="true" />} label={t("shell.settings")} onClick={() => openGlobalSettings()} testId="rail-settings" />
			</div>
		</nav>
	);
}
