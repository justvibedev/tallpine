import type { HostedNotification } from "../notificationView";
import type { NotificationRecord } from "../api";

export const PAGE_SIZE = 50;
export type FeedItem = HostedNotification<NotificationRecord>;
export type HostFeed = {
	hostId: string;
	hostName: string;
	items: NotificationRecord[];
	nextCursor?: string;
	unreadCount: number;
	error?: string;
};
