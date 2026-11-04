import { useCallback, useEffect } from "react";
import { getNotifications, markAllNotificationsRead } from "../api";
import { notificationIdentity } from "../notificationView";
import { userFacingError } from "../connectionError";
import { HostSnapshot } from "../otherHosts";
import { ServerConfig } from "../config";
import { type HostFeed, PAGE_SIZE } from "./notificationTypes";

export function useNotificationFeeds({
	setFeeds,
	feedsRef,
	loadingMoreRef,
	hostsRef,
	loadGeneration,
	acknowledgedRef,
	configForHost,
	loadedOnceRef,
	setLoading,
	setRefreshing,
	setLoadingMore,
	hostSignature,
	focused,
	feeds,
	ackAttemptRef,
	ackOwnerRef,
	setHighlightedIds,
	refreshHost,
	setNotice,
}: {
	setFeeds: React.Dispatch<React.SetStateAction<Record<string, HostFeed>>>;
	feedsRef: React.RefObject<Record<string, HostFeed>>;
	loadingMoreRef: React.RefObject<boolean>;
	hostsRef: React.RefObject<HostSnapshot[]>;
	loadGeneration: React.RefObject<number>;
	acknowledgedRef: React.RefObject<Set<string>>;
	configForHost: (hostId: string) => ServerConfig | null;
	loadedOnceRef: React.RefObject<boolean>;
	setLoading: React.Dispatch<React.SetStateAction<boolean>>;
	setRefreshing: React.Dispatch<React.SetStateAction<boolean>>;
	setLoadingMore: React.Dispatch<React.SetStateAction<boolean>>;
	hostSignature: string;
	focused: boolean;
	feeds: Record<string, HostFeed>;
	ackAttemptRef: React.RefObject<number>;
	ackOwnerRef: React.RefObject<Map<string, number>>;
	setHighlightedIds: React.Dispatch<React.SetStateAction<Set<string>>>;
	refreshHost: (hostId: string) => Promise<void>;
	setNotice: React.Dispatch<React.SetStateAction<string | undefined>>;
}) {

	const updateFeeds = useCallback((update: (current: Record<string, HostFeed>) => Record<string, HostFeed>) => {
		setFeeds((current) => {
			const next = update(current);
			feedsRef.current = next;
			return next;
		});
	}, []);

	const load = useCallback(
		async (mode: "initial" | "refresh" | "more") => {
			if (mode === "more" && loadingMoreRef.current) return;
			const hosts = hostsRef.current;
			const currentFeeds = feedsRef.current;
			const generation = mode === "more" ? loadGeneration.current : ++loadGeneration.current;
			// A reload may bring back rows whose ack failed; let them be retried.
			if (mode !== "more") acknowledgedRef.current = new Set();
			const targets = hosts.flatMap((host) => {
				const source = configForHost(host.hostId);
				const cursor = currentFeeds[host.hostId]?.nextCursor;
				if (!source || (mode === "more" && !cursor)) return [];
				return [{ host, source, cursor: mode === "more" ? cursor : undefined }];
			});
			if (mode === "initial" && !loadedOnceRef.current) setLoading(true);
			if (mode === "refresh") setRefreshing(true);
			if (mode === "more") {
				loadingMoreRef.current = true;
				setLoadingMore(true);
			}
			const results = await Promise.all(targets.map(async ({ host, source, cursor }) => {
				try {
					const page = await getNotifications(source, { status: "all", limit: PAGE_SIZE, cursor });
					return { host, source, page } as const;
				} catch (cause) {
					return { host, source, error: userFacingError(cause, `Couldn't load notifications from ${host.name}.`) } as const;
				}
			}));
			if (generation !== loadGeneration.current) return;
			updateFeeds((previous) => {
				const paired = new Set(hosts.map((host) => host.hostId));
				const next = Object.fromEntries(Object.entries(previous).filter(([hostId]) => paired.has(hostId)));
				for (const host of hosts) {
					if (configForHost(host.hostId)) continue;
					const retained = next[host.hostId];
					next[host.hostId] = {
						hostId: host.hostId,
						hostName: host.name,
						items: retained?.items ?? [],
						nextCursor: retained?.nextCursor,
						unreadCount: retained?.unreadCount ?? host.notificationsUnread,
						error: `${host.name} is offline.`,
					};
				}
				for (const result of results) {
					if (configForHost(result.host.hostId) !== result.source) continue;
					const previousFeed = next[result.host.hostId];
					if ("error" in result) {
						next[result.host.hostId] = {
							hostId: result.host.hostId,
							hostName: result.host.name,
							items: previousFeed?.items ?? [],
							nextCursor: previousFeed?.nextCursor,
							unreadCount: previousFeed?.unreadCount ?? result.host.notificationsUnread,
							error: result.error,
						};
						continue;
					}
					const priorItems = mode === "more" ? previousFeed?.items ?? [] : [];
					const seen = new Set(priorItems.map((item) => item.id));
					next[result.host.hostId] = {
						hostId: result.host.hostId,
						hostName: result.host.name,
						items: [...priorItems, ...result.page.notifications.filter((item) => !seen.has(item.id))],
						nextCursor: result.page.nextCursor,
						unreadCount: result.page.unreadCount,
					};
				}
				return next;
			});
			loadedOnceRef.current = true;
			setLoading(false);
			setRefreshing(false);
			loadingMoreRef.current = false;
			setLoadingMore(false);
		},
		[configForHost, updateFeeds],
	);

	useEffect(() => {
		void load("initial");
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [hostSignature]);

	// Concrete loaded ids only, never "everything": unread rows on pages not
	// loaded yet stay unread until the user scrolls to them. Only while focused:
	// a reload under an open session (a host reconnecting) must not mark rows
	// the user has not seen. They are acked, and highlighted, on return.
	useEffect(() => {
		if (!focused) return;
		for (const feed of Object.values(feeds)) {
			const source = feed.error ? null : configForHost(feed.hostId);
			if (!source) continue;
			const ids = feed.items
				.filter((item) => item.status === "unread" && !acknowledgedRef.current.has(notificationIdentity(feed.hostId, item.id)))
				.map((item) => item.id);
			if (ids.length === 0) continue;
			const keys = ids.map((id) => notificationIdentity(feed.hostId, id));
			const attempt = ++ackAttemptRef.current;
			for (const key of keys) {
				acknowledgedRef.current.add(key);
				ackOwnerRef.current.set(key, attempt);
			}
			setHighlightedIds((current) => new Set([...current, ...keys]));
			const acked = new Set(ids);
			updateFeeds((previous) => {
				const current = previous[feed.hostId];
				if (!current) return previous;
				const marked = current.items.filter((item) => acked.has(item.id) && item.status === "unread").length;
				return { ...previous, [feed.hostId]: {
					...current,
					items: current.items.map((item) => acked.has(item.id) ? { ...item, status: "read" } : item),
					unreadCount: Math.max(0, current.unreadCount - marked),
				} };
			});
			markAllNotificationsRead(source, ids).then(
				// Refresh that machine so the bell badge is already clear on the way back.
				// Its failure says nothing about the ack, so it must not roll rows back.
				() => { void refreshHost(feed.hostId).catch(() => {}); },
				() => {
					// Put back to unread only the rows this attempt still owns, without
					// forgetting the ack, so a machine that keeps refusing is not retried
					// in a loop. Pull-to-refresh retries.
					const owned = new Set(ids.filter((id) => ackOwnerRef.current.get(notificationIdentity(feed.hostId, id)) === attempt));
					if (owned.size === 0) return;
					setNotice(`Couldn't mark notifications read on ${feed.hostName}.`);
					updateFeeds((previous) => {
						const current = previous[feed.hostId];
						if (!current) return previous;
						const restored = current.items.filter((item) => owned.has(item.id) && item.status === "read").length;
						return { ...previous, [feed.hostId]: {
							...current,
							items: current.items.map((item) => owned.has(item.id) ? { ...item, status: "unread" } : item),
							unreadCount: current.unreadCount + restored,
						} };
					});
				},
			);
		}
	}, [configForHost, feeds, focused, refreshHost, updateFeeds]);

	return { updateFeeds, load };
}
