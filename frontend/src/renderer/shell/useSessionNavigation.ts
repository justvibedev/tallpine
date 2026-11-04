import type { UseNavigateResult } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useCallback } from "react";
import { useUiStore } from "../stores/ui-store";
import { type WorkspaceSummary } from "../types/workspace";
import { sessionNavigateTarget } from "../lib/navigate-to-session";
import {
	buildSidebarSessionOrder,
	stepSessionNav,
	type SidebarNavSession,
} from "../lib/sidebar-order";
import { sessionWantsAttention } from "../lib/session-presentation";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";

export function useSessionNavigation({ t, navigate, workspacesRef, remoteWorkspacesRef, routeParams }: {
	t: TFunction<"translation", undefined>;
	navigate: UseNavigateResult<string>;
	workspacesRef: React.RefObject<WorkspaceSummary[]>;
	remoteWorkspacesRef: React.RefObject<WorkspaceSummary[]>;
	routeParams: { hostId?: string | undefined; projectId?: string | undefined; sessionId?: string | undefined; };
}) {
	// Session shortcuts walk the sidebar's own order (pinned, projects in the
	// user's drag order, scratchpad), not the daemon's raw list, so "next" always
	// means the row below the current one on screen.
	const sidebarSessionOrder = useCallback(() => {
		const { projectOrder, sessionOrders } = useSidebarOrderStore.getState();
		return buildSidebarSessionOrder({
			workspaces: workspacesRef.current,
			remoteWorkspaces: remoteWorkspacesRef.current,
			projectOrder,
			sessionOrders,
		});
	}, []);
	const openNavSession = useCallback(
		(entry: SidebarNavSession) => {
			void navigate(sessionNavigateTarget(entry.projectId, entry.sessionId, entry.hostId));
		},
		[navigate],
	);
	const navigateSession = useCallback(
		(direction: -1 | 1, onlyNeedingAttention = false) => {
			// The settings page owns these chords while it is open (they step its sections).
			if (useUiStore.getState().settingsModal?.scope === "global") return;
			const target = stepSessionNav(
				sidebarSessionOrder(),
				{ sessionId: routeParams.sessionId, hostId: routeParams.hostId },
				direction,
				onlyNeedingAttention ? (entry) => sessionWantsAttention(entry.session) : undefined,
			);
			if (target) {
				openNavSession(target);
			} else if (onlyNeedingAttention) {
				useUiStore.getState().showGlobalToast(t("shortcut.noAttentionSessions"));
			}
		},
		[openNavSession, routeParams.hostId, routeParams.sessionId, sidebarSessionOrder, t],
	);
	return { sidebarSessionOrder, openNavSession, navigateSession };
}
