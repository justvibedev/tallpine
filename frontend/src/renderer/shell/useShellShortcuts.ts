import type { AnyRouter } from "@tanstack/react-router";
import type { NewTaskBacklog } from "../stores/ui-store";
import { useEffect } from "react";
import { tallpineBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";
import { STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { type SidebarNavSession } from "../lib/sidebar-order";
import { useAppShortcut } from "../hooks/useAppShortcut";

export function useShellShortcuts({ router, toggleSidebar, requestNewTask, setIsKeyboardShortcutsOpen, routeParams, scopedProjectId, sidebarSessionOrder, openNavSession, navigateSession }: {
	router: AnyRouter;
	toggleSidebar: () => void;
	requestNewTask: (projectId: string, hostId?: string | undefined, backlog?: NewTaskBacklog | undefined) => void;
	setIsKeyboardShortcutsOpen: React.Dispatch<React.SetStateAction<boolean>>;
	routeParams: { hostId?: string | undefined; projectId?: string | undefined; sessionId?: string | undefined; };
	scopedProjectId: string | undefined;
	sidebarSessionOrder: () => SidebarNavSession[];
	openNavSession: (entry: SidebarNavSession) => void;
	navigateSession: (direction: 1 | -1, onlyNeedingAttention?: boolean) => void;
}) {
	// These arrive from the main process (see main/app-shortcuts.ts) so they also
	// fire while xterm or a native Browser view holds focus.
	useAppShortcut("toggle-sidebar", () => toggleSidebar());
	useAppShortcut("jump-to-session", ({ index }) => {
		// The palette's own list claims the digits while it is open.
		if (useUiStore.getState().isCommandPaletteOpen) return;
		if (useUiStore.getState().settingsModal?.scope === "global") return;
		const target = index === undefined ? undefined : sidebarSessionOrder()[index];
		if (target) openNavSession(target);
	});
	useAppShortcut("previous-attention-session", () => navigateSession(-1, true));
	useAppShortcut("next-attention-session", () => navigateSession(1, true));
	useAppShortcut("history-back", () => {
		if (router.history.canGoBack()) router.history.back();
	});
	useAppShortcut("history-forward", () => router.history.forward());

	// New session (⌘N / Ctrl+Shift+N) is detected in the main process and
	// delivered here, so it fires even when focus is inside xterm or a native
	// Browser-preview view. The shell owns the routing: open the New Task flow
	// for the in-scope project, or a standalone agent when no project is in scope.
	useEffect(
		() =>
			tallpineBridge.app.onNewSessionShortcut(() => {
			if (routeParams.hostId) {
				if (routeParams.projectId) requestNewTask(routeParams.projectId, routeParams.hostId);
				else requestNewTask(STANDALONE_WORKSPACE_ID, routeParams.hostId);
				return;
			}
				if (scopedProjectId) {
					requestNewTask(scopedProjectId);
				} else {
					requestNewTask(STANDALONE_WORKSPACE_ID);
				}
			}),
		[routeParams.hostId, routeParams.projectId, scopedProjectId, requestNewTask],
	);

	// ⌘/ toggles: a second press closes the sheet.
	useEffect(() => tallpineBridge.app.onKeyboardShortcutsHelp(() => setIsKeyboardShortcutsOpen((open) => !open)), []);
}
