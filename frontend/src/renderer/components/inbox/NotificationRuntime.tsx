import { useQueryClient } from "@tanstack/react-query";
import { useParams } from "@tanstack/react-router";
import { useCallback, useEffect, useRef } from "react";
import { useNotificationsQuery } from "../../hooks/useNotificationsQuery";
import { tallpineBridge } from "../../lib/bridge";
import { parseRefKey, sessionUiKey, type HostId } from "../../lib/hosts";
import { createNotificationsTransport, getCachedNotifications, getCachedUnreadCount, type NotificationDTO, type NotificationsCache, type NotificationsPage, recentNotificationsQueryKey, unreadNotificationsQueryKey } from "../../lib/notifications";
import { useUiStore } from "../../stores/ui-store";
import { connectRemoteNotificationStreams, remoteNotificationsQueryKey, useRemoteNotificationHosts } from "../../hooks/useRemoteNotifications";

import { notificationKey } from "./notification-keys";
import { useNotificationTargetNavigation } from "./useNotificationTargetNavigation";
const REMOTE_NOTIFICATION_PREFIX = "remote-notification:";

export function NotificationRuntime() {
	const queryClient = useQueryClient();
	const { openPrimary } = useNotificationTargetNavigation();
	const unreadQuery = useNotificationsQuery("unread");
	const remoteUnread = useRemoteNotificationHosts("unread");
	const unreadCount = getCachedUnreadCount(unreadQuery.data) + remoteUnread.totalUnreadCount;
	const params = useParams({ strict: false }) as { hostId?: string; sessionId?: string };
	const routeRef = useRef(params);
	routeRef.current = params;
	const seenRemoteIds = useRef(new Map<HostId, Set<string>>());
	const shownRemoteNotifications = useRef(new Map<string, NotificationDTO>());

	// Being on the session route is not the same as watching the agent: its pane
	// renders one terminal at a time, so a shell or reviewer tab hides the agent
	// while the route is unchanged. Only report the session whose agent terminal
	// is the one on screen. Read the store imperatively — this feeds a getter for
	// the long-lived SSE connection, which needs the current value, not a render.
	const getVisibleAgentSessionId = useCallback(() => {
		const { hostId, sessionId } = routeRef.current;
		if (!sessionId || hostId) return undefined;
		return useUiStore.getState().visibleTerminalKindBySession[sessionId] === "worker" ? sessionId : undefined;
	}, []);

	useEffect(
		() => createNotificationsTransport(queryClient, getVisibleAgentSessionId).connect(),
		[getVisibleAgentSessionId, queryClient],
	);
	useEffect(() => connectRemoteNotificationStreams(queryClient), [queryClient]);

	// Keep the OS launcher badge in sync here rather than in NotificationCenter:
	// NotificationRuntime is always mounted in the shell, whereas the Inbox list
	// is only mounted on /inbox.
	useEffect(() => {
		void tallpineBridge.notifications.setBadge(unreadCount);
	}, [unreadCount]);

	useEffect(() => {
		for (const host of remoteUnread.hosts) {
			if (!host.data) continue;
			const currentIds = new Set(host.data.notifications.map((item) => item.id));
			const previousIds = seenRemoteIds.current.get(host.hostId);
			if (!previousIds) {
				seenRemoteIds.current.set(host.hostId, currentIds);
				continue; // Reconnecting to a host must not replay its existing inbox.
			}
			for (const notification of host.data.notifications) {
				if (previousIds.has(notification.id)) continue;
				const id = `${REMOTE_NOTIFICATION_PREFIX}${notificationKey(host.hostId, notification.id)}`;
				shownRemoteNotifications.current.set(id, notification);
				if (shownRemoteNotifications.current.size > 256) {
					shownRemoteNotifications.current.delete(shownRemoteNotifications.current.keys().next().value!);
				}
				const { hostId, sessionId } = routeRef.current;
				const watched = notification.type === "needs_input" && hostId === host.hostId &&
					sessionId === notification.sessionId && document.visibilityState === "visible" && document.hasFocus() &&
					useUiStore.getState().visibleTerminalKindBySession[sessionUiKey(sessionId, hostId)] === "worker";
				void tallpineBridge.notifications.show({
					id,
					title: `${host.label}: ${notification.title}`,
					body: notification.body || undefined,
					type: notification.type,
					urgency: notification.urgency,
					watched,
				});
			}
			for (const id of currentIds) {
				previousIds.delete(id);
				previousIds.add(id);
				if (previousIds.size > 512) previousIds.delete(previousIds.values().next().value!);
			}
		}
	}, [remoteUnread.hosts]);

	useEffect(() => {
		return tallpineBridge.notifications.onClick((id) => {
			if (id.startsWith(REMOTE_NOTIFICATION_PREFIX)) {
				try {
					const { host, id: notificationId } = parseRefKey(id.slice(REMOTE_NOTIFICATION_PREFIX.length));
					const unread = queryClient.getQueryData<NotificationsPage>(remoteNotificationsQueryKey(host, "unread"));
					const all = queryClient.getQueryData<NotificationsPage>(remoteNotificationsQueryKey(host, "all"));
					const notification = shownRemoteNotifications.current.get(id) ?? [...(unread?.notifications ?? []), ...(all?.notifications ?? [])].find((item) => item.id === notificationId);
					if (notification) openPrimary(notification, host);
				} catch { /* Ignore malformed or obsolete OS notification IDs. */ }
				return;
			}
			const unread = queryClient.getQueryData<NotificationsCache>(unreadNotificationsQueryKey);
			const recent = queryClient.getQueryData<NotificationsCache>(recentNotificationsQueryKey);
			const notification = [...getCachedNotifications(unread), ...getCachedNotifications(recent)].find(
				(item) => item.id === id,
			);
			if (notification) openPrimary(notification);
		});
	}, [openPrimary, queryClient]);

	return null;
}
