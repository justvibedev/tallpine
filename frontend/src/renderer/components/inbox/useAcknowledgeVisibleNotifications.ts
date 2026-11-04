import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, type Dispatch, type SetStateAction } from "react";
import { useTranslation } from "react-i18next";
import { markRemoteNotificationsRead } from "../../hooks/useRemoteNotifications";
import { LOCAL_HOST } from "../../lib/hosts";
import { getCachedNotifications, type NotificationDTO, type NotificationsCache } from "../../lib/notifications";
import { notificationKey } from "./notification-keys";

type RemoteItems = { hostId: string; label: string; notifications: NotificationDTO[] }[];

/**
 * While the Inbox list is showing, acknowledges what it has loaded: unread local rows are marked read
 * (and stay highlighted), as are remote ones. Later unread rows are acknowledged as they appear.
 */
export function useAcknowledgeVisibleNotifications({
	open,
	ackRetryNonce,
	unreadData,
	unreadLoading,
	notifications,
	remoteItems,
	markAllMutate,
	setHighlightedIds,
	setMarkReadError,
}: {
	open: boolean;
	ackRetryNonce: number;
	unreadData: NotificationsCache | undefined;
	unreadLoading: boolean;
	notifications: NotificationDTO[];
	remoteItems: RemoteItems;
	markAllMutate: (ids: string[]) => Promise<unknown>;
	setHighlightedIds: Dispatch<SetStateAction<Set<string>>>;
	setMarkReadError: Dispatch<SetStateAction<string | null>>;
}) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	// Concrete ids only — never `[]` — so unread pages past the first stay
	// reachable and arrive as unread (still highlighted) when the all-list loads them.
	const acknowledgedIdsRef = useRef<Set<string>>(new Set());
	const acknowledgedRemoteIdsRef = useRef<Set<string>>(new Set());
	const remoteHostsRef = useRef(remoteItems);
	remoteHostsRef.current = remoteItems;
	const visibleRemoteUnreadKey = remoteItems.flatMap((host) =>
		host.notifications.filter((item) => item.status === "unread")
			.map((item) => notificationKey(host.hostId, item.id)),
	).join("|");
	const visibleUnreadIds = useMemo(() => {
		const ids = new Set<string>();
		for (const item of getCachedNotifications(unreadData)) {
			if (item.status === "unread") ids.add(item.id);
		}
		for (const item of notifications) {
			if (item.status === "unread") ids.add(item.id);
		}
		return [...ids];
	}, [notifications, unreadData]);
	const visibleUnreadKey = visibleUnreadIds.join("|");

	// Opening the panel acknowledges what has actually been loaded. Later unread
	// rows are acknowledged as they appear, keeping the unread pagination cursor.
	useEffect(() => {
		if (!open) {
			setHighlightedIds(new Set());
			setMarkReadError(null);
			acknowledgedIdsRef.current = new Set();
			return;
		}
		if (unreadLoading) return;

		const visibleIds = visibleUnreadKey === "" ? [] : visibleUnreadKey.split("|");
		const newly = visibleIds.filter((id) => !acknowledgedIdsRef.current.has(id));
		if (newly.length === 0) return;

		for (const id of newly) acknowledgedIdsRef.current.add(id);
		setHighlightedIds((current) => {
			const next = new Set(current);
			let changed = false;
			for (const id of newly) {
				const key = notificationKey(LOCAL_HOST, id);
				if (next.has(key)) continue;
				next.add(key);
				changed = true;
			}
			return changed ? next : current;
		});

		setMarkReadError(null);
		void markAllMutate(newly)
			.catch((error: unknown) => {
				for (const id of newly) acknowledgedIdsRef.current.delete(id);
				setMarkReadError(error instanceof Error ? error.message : t("notify.couldNotMarkAllRead"));
			});
	}, [ackRetryNonce, markAllMutate, open, t, unreadLoading, visibleUnreadKey]);
	useEffect(() => {
		if (!open) {
			acknowledgedRemoteIdsRef.current.clear();
			return;
		}
		for (const host of remoteHostsRef.current) {
			if (host.notifications.length === 0) continue;
			const unreadIds = host.notifications
				.filter((item) => item.status === "unread")
				.map((item) => item.id)
				.filter((id) => !acknowledgedRemoteIdsRef.current.has(notificationKey(host.hostId, id)));
			if (unreadIds.length === 0) continue;
			for (const id of unreadIds) acknowledgedRemoteIdsRef.current.add(notificationKey(host.hostId, id));
			setHighlightedIds((current) => {
				const next = new Set(current);
				for (const id of unreadIds) next.add(notificationKey(host.hostId, id));
				return next;
			});
			void (async () => {
				try {
					await markRemoteNotificationsRead(host.hostId, unreadIds, queryClient);
				} catch (error) {
					for (const id of unreadIds) acknowledgedRemoteIdsRef.current.delete(notificationKey(host.hostId, id));
					setMarkReadError(`${host.label}: ${error instanceof Error ? error.message : t("notify.couldNotMarkAllRead")}`);
				}
			})();
		}
	}, [ackRetryNonce, open, queryClient, t, visibleRemoteUnreadKey]);
}
