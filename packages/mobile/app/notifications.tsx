import { Feather } from "../lib/icons";
import { useFocusEffect, useRouter } from "expo-router";
import { useIsFocused } from "expo-router/react-navigation";
import { useOpenPage } from "../lib/pageNavigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, RefreshControl, SectionList, Text, View } from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { haptics } from "../lib/haptics";
import { notificationIdentity, notificationsAcrossHosts, notificationSections, notificationAction } from "../lib/notificationView";
import { useApp } from "../lib/store";
import { MINUTE_MS, useNow } from "../lib/useNow";
import { useTheme, useThemedStyles } from "../lib/ThemeProvider";
import { Button, EmptyState, HeaderIconButton, ScreenHeader } from "../lib/ui";
import { UnpairedState } from "../lib/UnpairedState";
import { backOr } from "../lib/backNavigation";
import { machineAppearance } from "../lib/machineAppearance";
import { type HostFeed } from "../lib/notifications/notificationTypes";
import { useNotificationActions } from "../lib/notifications/useNotificationActions";
import { useNotificationFeeds } from "../lib/notifications/useNotificationFeeds";

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";

export type { FeedItem } from "../lib/notifications/notificationTypes";
import { NotificationRow, NotificationSectionHeader } from "../lib/notifications/NotificationParts";
import { makeStyles } from "../lib/notifications/notificationStyles";


// The durable record of what the daemon has notified about. Push only reaches a
// phone that was reachable at the time; this list is what the user can come back
// to afterwards.
export default function NotificationsScreen() {
	return <NotificationsContent />;
}

function NotificationsContent() {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const router = useRouter();
	const openPage = useOpenPage();
	const insets = useSafeAreaInsets();
	const { hostStates, configForHost, refreshHost, restore } = useApp();
	const [restoringId, setRestoringId] = useState<string>();
	// A brief line rather than an Alert: the row is still there to act on, and
	// a modal would make a dead tap feel like an error.
	const [notice, setNotice] = useState<string>();
	const now = useNow(MINUTE_MS);
	const [feeds, setFeeds] = useState<Record<string, HostFeed>>({});
	const feedsRef = useRef(feeds);
	feedsRef.current = feeds;
	const hostsRef = useRef(hostStates);
	hostsRef.current = hostStates;
	const loadGeneration = useRef(0);
	const loadingMoreRef = useRef(false);
	const loadedOnceRef = useRef(false);
	const [loading, setLoading] = useState(true);
	const [refreshing, setRefreshing] = useState(false);
	const [loadingMore, setLoadingMore] = useState(false);
	// Opening the screen marks what it loaded as read, as the desktop panel does.
	// Those rows keep their unread look for this visit so the user can still see
	// what was new.
	const [highlightedIds, setHighlightedIds] = useState<Set<string>>(() => new Set());
	const acknowledgedRef = useRef(new Set<string>());
	// Which ack attempt last claimed each row. A reload re-acks the same rows, so
	// an older attempt that fails late must not roll back a newer one.
	const ackOwnerRef = useRef(new Map<string, number>());
	const ackAttemptRef = useRef(0);
	// A visit ends when the screen loses focus, not when it unmounts: tapping into
	// a session leaves this screen mounted underneath, and coming back should
	// show those rows as read, like reopening the desktop panel.
	useFocusEffect(useCallback(() => () => setHighlightedIds(new Set()), []));
	const focused = useIsFocused();
	const items = useMemo(() => notificationsAcrossHosts(Object.values(feeds).map((feed) => {
		const host = hostStates.find((candidate) => candidate.hostId === feed.hostId);
		return {
			hostId: feed.hostId,
			hostName: feed.hostName,
			hostAppearance: machineAppearance({ id: feed.hostId, name: feed.hostName, appearance: host?.appearance }),
			items: feed.items,
		};
	})), [feeds, hostStates]);
	const sections = useMemo(() => notificationSections(items), [items]);
	const unavailableFeeds = useMemo(() => Object.values(feeds).filter((feed) => feed.error), [feeds]);
	const hasMore = useMemo(() => Object.values(feeds).some((feed) => Boolean(feed.nextCursor)), [feeds]);
	const knownHostIds = useMemo(() => new Set(hostStates.map((host) => host.hostId)), [hostStates]);
	const hostSignature = hostStates.map((host) => {
		const source = host.config;
		return `${host.hostId}:${source ? `${source.host}:${source.httpPort}:${source.secure ? "secure" : "plain"}` : "offline"}`;
	}).join("|");
	const { updateFeeds, load } = useNotificationFeeds({ setFeeds, feedsRef, loadingMoreRef, hostsRef, loadGeneration, acknowledgedRef, configForHost, loadedOnceRef, setLoading, setRefreshing, setLoadingMore, hostSignature, focused, feeds, ackAttemptRef, ackOwnerRef, setHighlightedIds, refreshHost, setNotice });
	const { open, clear, sessionState, restoreSession, confirmClearAll } = useNotificationActions({ configForHost, setNotice, updateFeeds, load, router, openPage, knownHostIds, hostStates, setRestoringId, restore, feedsRef, loadGeneration, refreshHost });

	useEffect(() => {
		if (!notice) return;
		const timer = setTimeout(() => setNotice(undefined), 2800);
		return () => clearTimeout(timer);
	}, [notice]);

	return (
		<View style={styles.screen}>
			<View style={{ height: insets.top }} />
			<ScreenHeader
				title="Notifications"
				left={<HeaderIconButton icon="back" label="Back" onPress={() => backOr(router)} />}
				right={
					items.length > 0 ? (
						<HeaderIconButton icon="trash" label="Clear all" onPress={confirmClearAll} />
					) : undefined
				}
			/>

			{loading ? (
				<View style={styles.center}>
					<ActivityIndicator color={t.accent} />
				</View>
			) : (
				<SectionList
					sections={sections}
					keyExtractor={(item) => notificationIdentity(item.hostId, item.id)}
					contentInsetAdjustmentBehavior="automatic"
					contentContainerStyle={
						items.length === 0
							? { flexGrow: 1 }
							: { paddingBottom: insets.bottom + 24 }
					}
					stickySectionHeadersEnabled={false}
					refreshControl={
						<RefreshControl
							refreshing={refreshing}
							onRefresh={() => {
								haptics.tap();
								void load("refresh");
							}}
							tintColor={t.accent}
						/>
					}
					onEndReached={() => { if (hasMore) void load("more"); }}
					onEndReachedThreshold={0.4}
					ListHeaderComponent={
						unavailableFeeds.length > 0 && items.length > 0 ? (
							<View style={styles.inlineError}>
								<Feather name="wifi-off" size={15} color={t.textSecondary} />
								<Text style={styles.inlineErrorText}>
									{unavailableFeeds.length === hostStates.length
										? `${unavailableFeeds.map((feed) => feed.hostName).join(", ")} ${unavailableFeeds.length === 1 ? "is" : "are"} offline. Showing previously loaded activity.`
										: `${unavailableFeeds.map((feed) => feed.hostName).join(", ")} ${unavailableFeeds.length === 1 ? "is" : "are"} unavailable. Showing activity from the available computers.`}
								</Text>
							</View>
						) : undefined
					}
					renderSectionHeader={({ section }) => (
						section.title
							? <NotificationSectionHeader title={section.title} count={section.data.length} />
							: null
					)}
					renderItem={({ item }) => (
						<NotificationRow
							item={item}
							highlighted={highlightedIds.has(notificationIdentity(item.hostId, item.id))}
							now={now}
							action={notificationAction(item, sessionState(item.hostId, item.sessionId)).kind}
							restoring={restoringId === notificationIdentity(item.hostId, item.sessionId)}
							onPress={() => open(item)}
							onRestore={() => item.sessionId && restoreSession(item.hostId, item.sessionId)}
							onClear={() => clear(item)}
						/>
					)}
					ListFooterComponent={
						loadingMore ? (
							<View style={styles.footer}>
								<ActivityIndicator color={t.accent} />
							</View>
						) : undefined
					}
					ListEmptyComponent={
						hostStates.length > 0 && unavailableFeeds.length === hostStates.length ? (
							<EmptyState
								icon="wifi-off"
								title="Computers are offline"
								message="Notifications will appear when at least one paired computer reconnects."
								action={<Button title="Retry" icon="refresh-cw" variant="ghost" onPress={() => void load("refresh")} />}
							/>
						) : hostStates.length === 0 ? (
							// Shared with the tabs: "Connecting…" while the launch race runs,
							// the pairing prompt only once it has found no machine.
							<UnpairedState />
						) : (
							<EmptyState
								icon="check-circle"
								title="All caught up"
								message="Updates from workers and pull requests across your computers will appear here when they need you."
							/>
						)
					}
				/>
			)}

			{/* Sits above the list rather than replacing it: the row that prompted
			    this is still on screen and still has a restore button to press. */}
			{notice ? (
				<View pointerEvents="none" style={[styles.notice, { bottom: insets.bottom + 24 }]}>
					<Feather name="alert-circle" size={15} color={t.amber} />
					<Text style={styles.noticeText}>{notice}</Text>
				</View>
			) : null}
		</View>
	);
}
