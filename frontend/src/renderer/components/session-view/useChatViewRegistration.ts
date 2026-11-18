import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { clientForSessionHost } from "../../lib/host-clients";
import { conversationQueryKey, refreshConversation } from "../../hooks/conversation";
import { workspaceQueryKeyForHost } from "../../hooks/useWorkspaceQuery";

/**
 * Registers this window as a viewer of a Chat so the daemon keeps (or wakes) its
 * controller, renewing every 10s and releasing on leave. The first successful
 * registration refreshes the conversation only if no snapshot newer than the
 * request exists, so a fresh open reads the conversation once.
 */
export function useChatViewRegistration(sessionId: string, hostId: string | undefined, active: boolean) {
	const queryClient = useQueryClient();
	useEffect(() => {
		if (!active) return;
		const viewId = crypto.randomUUID();
		let left = false;
		let refreshed = false;
		let pending = Promise.resolve();
		const setViewActive = async (active: boolean) => {
			const requestedAt = Date.now();
			try {
				const { error } = await clientForSessionHost(hostId).POST("/api/v1/sessions/{sessionId}/chat-view", {
					params: { path: { sessionId } },
					body: { viewId, active },
				});
				if (error) throw error;
				// Remote conversations already refresh every two seconds.
				if (active && !left && !refreshed && !hostId) {
					refreshed = true;
					void refreshConversation(queryClient, sessionId, hostId, requestedAt);
					void queryClient.invalidateQueries({ queryKey: workspaceQueryKeyForHost(hostId) });
				}
			} finally {
				// Release a late registration too; leaving must not wait on wake.
				if (active && left) void setViewActive(false).catch(() => {});
			}
		};
		const renewView = () => {
			pending = pending.catch(() => {}).then(() => {
				return left ? undefined : setViewActive(true);
			});
			return pending;
		};
		const refreshAfterWakeError = () => {
			if (left || hostId) return;
			void queryClient.invalidateQueries({ queryKey: conversationQueryKey(sessionId, hostId) });
		};
		void renewView().catch(refreshAfterWakeError);
		const renewal = window.setInterval(() => { void renewView().catch(refreshAfterWakeError); }, 10_000);
		return () => {
			left = true;
			window.clearInterval(renewal);
			void setViewActive(false).catch(() => {});
		};
	}, [active, hostId, queryClient, sessionId]);
}
