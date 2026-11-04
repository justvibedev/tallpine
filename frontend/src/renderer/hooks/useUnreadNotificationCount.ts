import { getCachedUnreadCount } from "../lib/notifications";
import { useNotificationsQuery } from "./useNotificationsQuery";
import { useRemoteNotificationHosts } from "./useRemoteNotifications";

/** Unread notifications across this machine and connected hosts; the sidebar toggle's badge. */
export function useUnreadNotificationCount(): number {
	const unreadQuery = useNotificationsQuery("unread");
	const remoteUnread = useRemoteNotificationHosts("unread");
	return getCachedUnreadCount(unreadQuery.data) + remoteUnread.totalUnreadCount;
}
