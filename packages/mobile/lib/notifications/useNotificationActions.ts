import { useRouter, type Href } from "expo-router";
import { useOpenPage } from "../pageNavigation";
import { Alert } from "react-native";
import { clearAllNotifications, clearNotification, markNotificationRead } from "../api";
import { haptics } from "../haptics";
import { notificationIdentity, notificationAction, notificationTarget } from "../notificationView";
import { userFacingError } from "../connectionError";
import { HostSnapshot } from "../otherHosts";
import { ServerConfig } from "../config";
import { type FeedItem, type HostFeed } from "./notificationTypes";

export function useNotificationActions({
	configForHost,
	setNotice,
	updateFeeds,
	load,
	router,
	openPage,
	knownHostIds,
	hostStates,
	setRestoringId,
	restore,
	feedsRef,
	loadGeneration,
	refreshHost,
}: {
	configForHost: (hostId: string) => ServerConfig | null;
	setNotice: React.Dispatch<React.SetStateAction<string | undefined>>;
	updateFeeds: (update: (current: Record<string, HostFeed>) => Record<string, HostFeed>) => void;
	load: (mode: "initial" | "refresh" | "more") => Promise<void>;
	router: ReturnType<typeof useRouter>;
	openPage: (href: Parameters<ReturnType<typeof useOpenPage>>[0]) => void;
	knownHostIds: Set<string>;
	hostStates: HostSnapshot[];
	setRestoringId: React.Dispatch<React.SetStateAction<string | undefined>>;
	restore: (id: string, hostId?: string | undefined) => Promise<void>;
	feedsRef: React.RefObject<Record<string, HostFeed>>;
	loadGeneration: React.RefObject<number>;
	refreshHost: (hostId: string) => Promise<void>;
}) {

	function open(notification: FeedItem) {
		const source = configForHost(notification.hostId);
		if (!source) {
			haptics.warning();
			setNotice(`${notification.hostName} is offline.`);
			return;
		}
		haptics.tap();
		updateFeeds((previous) => {
			const feed = previous[notification.hostId];
			if (!feed) return previous;
			return { ...previous, [notification.hostId]: {
				...feed,
				items: feed.items.map((item) => item.id === notification.id ? { ...item, status: "read" } : item),
				unreadCount: notification.status === "unread" ? Math.max(0, feed.unreadCount - 1) : feed.unreadCount,
			} };
		});
		if (notification.status === "unread") {
			markNotificationRead(source, notification.id).catch(() => void load("refresh"));
		}
		// What a tap does depends on the session behind it, exactly as the renderer
		// decides: a terminated agent waiting on input is restored, not opened.
		const action = notificationAction(notification, sessionState(notification.hostId, notification.sessionId));
		if (action.kind === "open") {
			// A needs-input row may be a permission prompt: answer it in the sheet,
			// which opens the session itself when there is nothing to answer.
			if (notification.type === "needs_input") router.navigate({ pathname: "/sheets/approval", params: { sessionId: action.sessionId, hostId: notification.hostId } });
			else router.navigate({ pathname: "/session/[id]", params: { id: action.sessionId, hostId: notification.hostId } });
		}
		else if (action.kind === "review") openPage(notificationTarget(notification, knownHostIds) as Href);
		else if (action.kind === "plan") router.navigate({ pathname: "/backlog", params: { projectId: action.projectId, hostId: notification.hostId } });
		else if (action.kind === "prs") router.navigate({ pathname: "/prs", params: { hostId: notification.hostId } });
		else if (action.kind === "restore") {
			haptics.warning();
			setNotice("This session is terminated. Tap restore to bring it back.");
		} else if (action.kind === "none") {
			haptics.warning();
			setNotice("That session is not available yet.");
		}
	}

	function clear(notification: FeedItem) {
		const source = configForHost(notification.hostId);
		if (!source) {
			haptics.warning();
			setNotice(`${notification.hostName} is offline.`);
			return false;
		}
		haptics.warning();
		updateFeeds((previous) => {
			const feed = previous[notification.hostId];
			if (!feed || !feed.items.some((item) => item.id === notification.id)) return previous;
			return { ...previous, [notification.hostId]: {
				...feed,
				items: feed.items.filter((item) => item.id !== notification.id),
				unreadCount: notification.status === "unread" ? Math.max(0, feed.unreadCount - 1) : feed.unreadCount,
			} };
		});
		void clearNotification(source, notification.id).catch((cause) => {
			haptics.error();
			setNotice(userFacingError(cause, "Couldn't clear that notification."));
			updateFeeds((previous) => {
				const feed = previous[notification.hostId];
				if (!feed || feed.items.some((item) => item.id === notification.id)) return previous;
				return { ...previous, [notification.hostId]: {
					...feed,
					items: [notification, ...feed.items],
					unreadCount: notification.status === "unread" ? feed.unreadCount + 1 : feed.unreadCount,
				} };
			});
		});
		return true;
	}

	function sessionState(hostId: string, sessionId?: string) {
		const host = hostStates.find((item) => item.hostId === hostId);
		const session = sessionId ? host?.sessions.find((item) => item.id === sessionId) : undefined;
		return {
			terminated: Boolean(session?.isTerminated || session?.status === "terminated"),
			// Without the board we cannot tell a terminated session from a live one,
			// and guessing lands on a screen that cannot resolve it. A machine that
			// pages its archive has answered once it reports the archive's size,
			// even with no live workers. Its archived workers are not in `sessions`,
			// so one of those offers Open, whose screen offers Restore.
			sessionsReady: Boolean(host && !host.loading && (host.sessions.length > 0 || host.archive)),
		};
	}

	function restoreSession(hostId: string, sessionId: string) {
		const key = notificationIdentity(hostId, sessionId);
		haptics.tap();
		setRestoringId(key);
		void restore(sessionId, hostId)
			.then(() => {
				haptics.success();
				router.navigate({ pathname: "/session/[id]", params: { id: sessionId, hostId } });
			})
			.catch((cause) => Alert.alert("Couldn't restore the session", userFacingError(cause)))
			.finally(() => setRestoringId(undefined));
	}

	function confirmClearAll() {
		Alert.alert("Clear all notifications?", "This removes the notification history on every connected computer.", [
			{ text: "Cancel", style: "cancel" },
			{ text: "Clear all", style: "destructive", onPress: () => void clearAll() },
		]);
	}

	// Mirrors the desktop panel's Clear all: each machine's whole history goes,
	// including pages this screen never loaded.
	async function clearAll() {
		const feedList = Object.values(feedsRef.current);
		const targets = feedList.flatMap((feed) => {
			const source = configForHost(feed.hostId);
			return source ? [{ feed, source }] : [];
		});
		const offline = feedList.filter((feed) => !configForHost(feed.hostId) && feed.items.length > 0);
		if (targets.length === 0) {
			haptics.warning();
			setNotice("Notifications are on computers that are currently offline.");
			return;
		}
		haptics.warning();
		// Drop anything in flight so a page that lands now cannot bring rows back.
		loadGeneration.current += 1;
		// Each emptied feed is kept by identity, so a failed clear can put the old
		// rows back without overwriting anything a reload has written since. The
		// recovery reload below may fail too, and it keeps whatever feed it finds.
		const cleared = new Map(targets.map(({ feed }) => [feed.hostId, { ...feed, items: [], nextCursor: undefined, unreadCount: 0 }]));
		updateFeeds((previous) => Object.fromEntries(Object.entries(previous).map(([hostId, feed]) => [hostId, cleared.get(hostId) ?? feed])));
		const results = await Promise.allSettled(targets.map(async ({ feed, source }) => {
			try {
				await clearAllNotifications(source);
			} catch (cause) {
				updateFeeds((previous) => previous[feed.hostId] === cleared.get(feed.hostId) ? { ...previous, [feed.hostId]: feed } : previous);
				throw new Error(userFacingError(cause, `Couldn't clear notifications on ${feed.hostName}.`));
			}
			void refreshHost(feed.hostId);
		}));
		const failure = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
		if (failure) {
			haptics.error();
			setNotice(failure.reason instanceof Error ? failure.reason.message : "Couldn't clear notifications.");
		} else if (offline.length > 0) {
			setNotice(`${offline.map((feed) => feed.hostName).join(", ")} ${offline.length === 1 ? "is" : "are"} offline, so ${offline.length === 1 ? "its" : "their"} notifications were not cleared.`);
		}
		// Also releases pagination that the generation bump cut off.
		void load("refresh");
	}

	return { open, clear, sessionState, restoreSession, confirmClearAll };
}
