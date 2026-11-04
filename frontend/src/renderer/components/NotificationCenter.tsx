import { useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { CheckCheck, CircleAlert, Inbox, LoaderCircle } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useClearAllNotificationsMutation, useClearNotificationMutation, useMarkAllNotificationsReadMutation, useNotificationsQuery } from "../hooks/useNotificationsQuery";
import { useRestoreSession } from "../hooks/useRestoreSession";
import { LOCAL_HOST, sessionUiKey, type HostId } from "../lib/hosts";
import { getCachedNotifications, isNotificationsCacheFromClear, keepLatestNotificationsPage, type NotificationDTO, type NotificationsPage, recentNotificationsQueryKey, unreadNotificationsQueryKey } from "../lib/notifications";
import { clearRemoteNotification, clearRemoteNotifications, fetchRemoteNotificationsPage, remoteNotificationsQueryKey, useRemoteNotificationHosts } from "../hooks/useRemoteNotifications";

import { useAcknowledgeVisibleNotifications } from "./inbox/useAcknowledgeVisibleNotifications";
import { NotificationEmpty, NotificationItem } from "./inbox/NotificationItem";
import { notificationKey } from "./inbox/notification-keys";
import { useNotificationTargetNavigation } from "./inbox/useNotificationTargetNavigation";
import { NotificationWorkspaceState } from "./inbox/NotificationWorkspaceState";
import { Spinner } from "./ui/spinner";
type NotificationCenterProps = {
	/** The Inbox list is showing; history loads while it is and trims when it stops. */
	active?: boolean;
	/** When set, a local notification row calls this instead of opening its session (the Inbox shows it in the center). */
	onSelect?: (notification: NotificationDTO) => void;
};

export function NotificationCenter({ active = false, onSelect }: NotificationCenterProps) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [actionError, setActionError] = useState<string | null>(null);
	const [markReadError, setMarkReadError] = useState<string | null>(null);
	// Bumped by the mark-read Retry control so a failed ack can run again even
	// when visibleUnreadKey is unchanged (removing ids from the ref alone does not).
	const [ackRetryNonce, setAckRetryNonce] = useState(0);
	const open = active;
	// Opening marks unread as read, which would drop the highlight under the
	// cursor. Keep the open-time unread ids highlighted until the panel closes.
	const [highlightedIds, setHighlightedIds] = useState<Set<string>>(() => new Set());
	const [clearingNotificationIds, setClearingNotificationIds] = useState<Set<string>>(() => new Set());
	const [restoringSessionId, setRestoringSessionId] = useState<string | undefined>();
	const [clearingAll, setClearingAll] = useState(false);
	const [remoteOlderPages, setRemoteOlderPages] = useState<Record<string, NotificationsPage[]>>({});
	const [remoteLoadingEarlierHost, setRemoteLoadingEarlierHost] = useState<string | null>(null);
	const [remoteEarlierError, setRemoteEarlierError] = useState<string | null>(null);
	const remoteHistoryGeneration = useRef(0);
	const remoteFirstPageKeys = useRef(new Map<string, string>());
	const unreadQuery = useNotificationsQuery("unread");
	const allQuery = useNotificationsQuery("all", open);
	const remoteUnread = useRemoteNotificationHosts("unread");
	const remoteAll = useRemoteNotificationHosts("all", open);
	const markAllRead = useMarkAllNotificationsReadMutation();
	const clearAll = useClearAllNotificationsMutation();
	const clearOne = useClearNotificationMutation();
	const restoreSession = useRestoreSession();
	const notifications = useMemo(() => getCachedNotifications(allQuery.data), [allQuery.data]);
	const remoteItems = useMemo(() => remoteAll.hosts.map((host) => {
		const seen = new Set<string>();
		const unread = remoteUnread.hosts.find((item) => item.hostId === host.hostId)?.data;
		const items = [unread, host.data, ...(remoteOlderPages[host.hostId] ?? [])].filter((page): page is NotificationsPage => Boolean(page)).flatMap((page) =>
			page.notifications.filter((notification) => {
				if (seen.has(notification.id)) return false;
				seen.add(notification.id);
				return true;
			}),
		);
		return { ...host, notifications: items };
	}), [remoteAll.hosts, remoteOlderPages, remoteUnread.hosts]);
	useEffect(() => {
		for (const host of remoteAll.hosts) {
			if (!host.data) continue;
			const key = JSON.stringify([host.data.notifications.map((item) => item.id), host.data.nextCursor]);
			const previous = remoteFirstPageKeys.current.get(host.hostId);
			remoteFirstPageKeys.current.set(host.hostId, key);
			if (previous === undefined || previous === key) continue;
			if (!(remoteOlderPages[host.hostId]?.length) && remoteLoadingEarlierHost !== host.hostId) continue;
			// Cursor pagination shifts when the first page changes; reload older pages from its new boundary.
			remoteHistoryGeneration.current += 1;
			setRemoteOlderPages((current) => ({ ...current, [host.hostId]: [] }));
			setRemoteLoadingEarlierHost(null);
		}
	}, [remoteAll.hosts, remoteLoadingEarlierHost, remoteOlderPages]);
	const entries = useMemo(() => [
		...notifications.map((notification) => ({ hostId: LOCAL_HOST, label: "", notification })),
		...remoteItems.flatMap((host) => host.notifications.map((notification) => ({
			hostId: host.hostId, label: host.label, notification,
		}))),
	].sort((a, b) => b.notification.createdAt.localeCompare(a.notification.createdAt)), [notifications, remoteItems]);
	const confirmedClearSnapshot = isNotificationsCacheFromClear(queryClient);
	const { openSession } = useNotificationTargetNavigation();
	const markAllMutate = markAllRead.mutateAsync;

	useAcknowledgeVisibleNotifications({
		open,
		ackRetryNonce,
		unreadData: unreadQuery.data,
		unreadLoading: unreadQuery.isLoading,
		notifications,
		remoteItems,
		markAllMutate,
		setHighlightedIds,
		setMarkReadError,
	});

	// Closing trims what the open panel paged in: remote older pages and the cached unread/recent pages.
	const resetLoadedHistory = useCallback(() => {
		remoteHistoryGeneration.current += 1;
		remoteFirstPageKeys.current.clear();
		setRemoteOlderPages({});
		setRemoteLoadingEarlierHost(null);
		setRemoteEarlierError(null);
		keepLatestNotificationsPage(queryClient, unreadNotificationsQueryKey);
		keepLatestNotificationsPage(queryClient, recentNotificationsQueryKey);
	}, [queryClient]);
	// Leaving the Inbox (or closing the sidebar) is the list's close.
	const wasActive = useRef(false);
	useEffect(() => {
		if (wasActive.current && !active) resetLoadedHistory();
		wasActive.current = active;
	}, [active, resetLoadedHistory]);
	// Switching sections unmounts the list in the same commit, so the effect above never sees `active` go false.
	const resetOnUnmount = useRef(resetLoadedHistory);
	resetOnUnmount.current = resetLoadedHistory;
	useEffect(() => () => {
		if (wasActive.current) resetOnUnmount.current();
	}, []);

	const retryMarkRead = () => {
		setMarkReadError(null);
		setAckRetryNonce((nonce) => nonce + 1);
	};

	const openSessionAndDismiss = useCallback((notification: NotificationDTO, hostId: HostId) => {
		if (onSelect && hostId === LOCAL_HOST) return onSelect(notification);
		openSession(notification, hostId);
	}, [onSelect, openSession]);

	const restoreThenOpen = useCallback(async (
		sessionId: string,
		hostId: HostId | undefined,
		restoreKey: string,
		openAfterRestore: () => void,
	) => {
		if (restoringSessionId) return;
		setRestoringSessionId(restoreKey);
		setActionError(null);
		try {
			const result = hostId && hostId !== LOCAL_HOST
				? await restoreSession(sessionId, hostId)
				: await restoreSession(sessionId);
			if (result.status === "success") {
				openAfterRestore();
				return;
			}
			setActionError(result.status === "not_resumable" ? t("notify.restoreUnavailable") : result.message);
		} finally {
			setRestoringSessionId(undefined);
		}
	}, [restoreSession, restoringSessionId, t]);

	const restoreAndOpen = useCallback((notification: NotificationDTO, hostId: HostId) => {
		const sessionId = notification.target.sessionId || notification.sessionId;
		if (!sessionId) return;
		void restoreThenOpen(sessionId, hostId, sessionUiKey(sessionId, hostId), () => openSession(notification, hostId));
	}, [openSession, restoreThenOpen]);

	// Clear local history unless it is confirmed empty. A failed local load still
	// clears server-side: the daemon's clear-all does not need rows loaded.
	const clearLocal = notifications.length > 0 || !allQuery.isSuccess;
	const handleClearAll = useCallback(() => {
		if (clearingAll) return;
		setActionError(null);
		setClearingAll(true);
		remoteHistoryGeneration.current += 1;
		setRemoteOlderPages({});
		setRemoteLoadingEarlierHost(null);
		setRemoteEarlierError(null);
		void Promise.allSettled([
			...(clearLocal ? [clearAll.mutateAsync()] : []),
			...remoteAll.hosts.map(async (host) => {
				await clearRemoteNotifications(host.hostId, queryClient).catch((error: unknown) => {
					throw new Error(`${host.label}: ${error instanceof Error ? error.message : t("notify.couldNotClearAll")}`);
				});
				setRemoteOlderPages((current) => ({ ...current, [host.hostId]: [] }));
			}),
		]).then((results) => {
			const failure = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
			if (failure) setActionError(failure.reason instanceof Error ? failure.reason.message : t("notify.couldNotClearAll"));
		}).finally(() => setClearingAll(false));
	}, [clearAll, clearLocal, clearingAll, queryClient, remoteAll.hosts, t]);

	const handleClear = useCallback((notification: NotificationDTO, hostId: HostId) => {
		setActionError(null);
		const key = notificationKey(hostId, notification.id);
		setClearingNotificationIds((current) => new Set(current).add(key));
		void (hostId === LOCAL_HOST ? clearOne.mutateAsync(notification) : (async () => {
			await clearRemoteNotification(hostId, notification.id, queryClient);
			remoteHistoryGeneration.current += 1;
			setRemoteLoadingEarlierHost(null);
			setRemoteOlderPages((current) => ({
				...current,
				[hostId]: (current[hostId] ?? []).map((page) => ({
					...page, notifications: page.notifications.filter((item) => item.id !== notification.id),
				})),
			}));
		})())
			.catch((error: unknown) => {
				setActionError(error instanceof Error ? error.message : t("notify.couldNotClearOne"));
			})
			.finally(() => {
				setClearingNotificationIds((current) => {
					const next = new Set(current);
					next.delete(key);
					return next;
				});
			});
	}, [clearOne, queryClient, t]);

	const loadEarlierOnScroll = (event: React.UIEvent<HTMLDivElement>) => {
		const list = event.currentTarget;
		const remaining = list.scrollHeight - list.scrollTop - list.clientHeight;
		if (remaining > 80 || !allQuery.hasNextPage || allQuery.isFetchingNextPage) return;
		void allQuery.fetchNextPage();
	};
	const loadRemoteEarlier = async (hostId: string, cursor: string) => {
		if (remoteLoadingEarlierHost) return;
		const generation = remoteHistoryGeneration.current;
		setRemoteLoadingEarlierHost(hostId);
		setRemoteEarlierError(null);
		try {
			const page = await fetchRemoteNotificationsPage(hostId, "all", cursor);
			if (generation === remoteHistoryGeneration.current) {
				setRemoteOlderPages((current) => ({ ...current, [hostId]: [...(current[hostId] ?? []), page] }));
			}
		} catch (error) {
			if (generation === remoteHistoryGeneration.current) {
				setRemoteEarlierError(error instanceof Error ? error.message : t("notify.earlierLoadFailed"));
			}
		} finally {
			if (generation === remoteHistoryGeneration.current) setRemoteLoadingEarlierHost(null);
		}
	};
	// Local history only loads once the panel opens. Hold the list until it arrives so rows don't pop in.
	const localEmpty = notifications.length === 0;
	const localLoadFailed = allQuery.isError && localEmpty && !confirmedClearSnapshot;
	const localLoading = allQuery.isLoading && localEmpty;
	const remoteLoadFailed = remoteAll.hosts.some((host) => host.isError);
	const remoteLoading = remoteAll.hosts.some((host) => host.isLoading);
	const rows = entries;
	const sessionRefs = useMemo(
		() => rows.flatMap(({ hostId, notification }) => {
			const sessionId = notification.target.sessionId || notification.sessionId;
			return sessionId ? [{ sessionId, hostId }] : [];
		}),
		[rows],
	);
	const isEmpty = rows.length === 0;

	const body = (
		<>
				<div className="flex items-center justify-between gap-2 border-b border-border bg-[var(--color-overlay-subtle)] px-4 py-3.5">
					<p className="text-subtitle font-semibold tracking-tight text-foreground">{t("notify.title")}</p>
					<button
						className="shrink-0 text-caption font-medium text-muted-foreground transition-colors hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
						disabled={isEmpty || localLoading || clearingAll || clearAll.isPending}
						onClick={handleClearAll}
						type="button"
					>
						{t("notify.clearAll")}
					</button>
				</div>
				<NotificationWorkspaceState sessionRefs={sessionRefs}>
					{({ retryWorkspace, loadedRemoteSessionIds, lookupPendingIds, sessionMeta, sessionsReady, terminatedIds, workspaceError }) => (
						<>
							{markReadError ? (
					<div
						aria-live="polite"
						className="flex items-center justify-between gap-2 border-b border-border bg-error/5 px-4 py-2 text-caption text-error"
					>
						<span>{markReadError}</span>
						<button
							className="shrink-0 font-medium underline underline-offset-2 hover:text-foreground"
							onClick={retryMarkRead}
							type="button"
						>
							{t("notify.retry")}
						</button>
					</div>
				) : null}
				{actionError ? (
					<div className="border-b border-border bg-error/5 px-4 py-2 text-caption text-error">{actionError}</div>
				) : null}
				{workspaceError ? (
					<div
						aria-live="polite"
						className="flex items-center justify-between gap-2 border-b border-border bg-error/5 px-4 py-2 text-caption text-error"
					>
						<span>{t("notify.workspaceLoadFailed")}</span>
						<button
							className="shrink-0 font-medium underline underline-offset-2 hover:text-foreground"
							onClick={retryWorkspace}
							type="button"
						>
							{t("notify.retry")}
						</button>
					</div>
				) : null}
				{(localLoadFailed || remoteLoadFailed) && !isEmpty ? (
					<div
						aria-live="polite"
						className="flex items-center justify-between gap-2 border-b border-border bg-error/5 px-4 py-2 text-caption text-error"
					>
						<span>{t("notify.loadFailed")}</span>
						<button
							className="shrink-0 font-medium underline underline-offset-2 hover:text-foreground"
							onClick={() => {
								if (localLoadFailed) void allQuery.refetch();
								for (const host of remoteAll.hosts) {
									if (host.isError) void queryClient.invalidateQueries({ queryKey: remoteNotificationsQueryKey(host.hostId, "all") });
								}
							}}
							type="button"
						>
							{t("notify.retry")}
						</button>
					</div>
				) : null}
				{(localLoadFailed || remoteLoadFailed) && isEmpty ? (
					<NotificationEmpty icon={CircleAlert} message={t("notify.loadFailed")} />
				) : localLoading || (remoteLoading && isEmpty) ? (
					<NotificationEmpty icon={Inbox} message={t("notify.loading")} />
				) : isEmpty ? (
					<NotificationEmpty icon={CheckCheck} message={t("notify.emptyAll")} />
					) : (
						<>
						<div
						aria-busy={allQuery.isFetchingNextPage || remoteLoadingEarlierHost !== null}
						className="board-scrollbar max-h-notification-max-height overflow-y-auto overscroll-contain py-1.5"
						onScroll={loadEarlierOnScroll}
						role="list"
					>
						{rows.map((row) => {
							const { hostId, label, notification } = row;
							const sessionId = notification.target.sessionId || notification.sessionId;
							const sessionKey = sessionId ? sessionUiKey(sessionId, hostId) : undefined;
							const sessionReady = (hostId === LOCAL_HOST ? sessionsReady : Boolean(sessionKey && loadedRemoteSessionIds.has(sessionKey))) && !(sessionKey && lookupPendingIds.has(sessionKey));
							const meta = sessionKey ? sessionMeta.get(sessionKey) : undefined;
							const terminated = Boolean(sessionKey) && terminatedIds.has(sessionKey ?? "");
							const key = notificationKey(hostId, notification.id);
							// Restoring only makes sense when an agent is actually paused waiting
							// on input. PR outcomes (ready_to_merge, pr_merged, pr_closed_unmerged)
							// describe work that already finished — there is nothing to resume, so
							// a terminated session behind one of these should stay viewable, not
							// gated behind a restore action.
							const offerRestore = sessionReady && terminated && notification.type === "needs_input";
							return (
								<NotificationItem
									highlighted={highlightedIds.has(key) || notification.status === "unread"}
									key={key}
									notification={notification}
									onOpenSession={() => openSessionAndDismiss(notification, hostId)}
									onRestore={() => restoreAndOpen(notification, hostId)}
									onClear={() => handleClear(notification, hostId)}
									clearing={clearingNotificationIds.has(key)}
									clearDisabled={clearingNotificationIds.has(key) || clearingAll}
									restoring={restoringSessionId === sessionKey}
									restoreDisabled={restoringSessionId !== undefined}
									hostLabel={label}
									projectName={meta?.projectName}
									sessionName={meta?.sessionName}
									sessionsReady={sessionReady}
									terminated={terminated}
									offerRestore={offerRestore}
								/>
							);
						})}
						{allQuery.isFetchNextPageError ? (
							<div
								aria-live="polite"
								className="flex items-center justify-center gap-2 px-4 py-3 text-caption text-error"
							>
								{t("notify.earlierLoadFailed")}
								<button
									className="font-medium underline underline-offset-2 hover:text-foreground"
									onClick={() => void allQuery.fetchNextPage()}
									type="button"
								>
									{t("notify.retry")}
								</button>
							</div>
						) : allQuery.isFetchingNextPage ? (
							<div
								aria-live="polite"
								className="flex items-center justify-center gap-2 px-4 py-3 text-caption text-passive"
							>
								<Spinner icon={LoaderCircle} className="size-icon-md" />
								{t("notify.loadingEarlier")}
							</div>
						) : null}
						{remoteAll.hosts.map((host) => {
							const older = remoteOlderPages[host.hostId] ?? [];
							const cursor = older.length > 0 ? older[older.length - 1].nextCursor : host.data?.nextCursor;
							if (!cursor || !host.data) return null;
							return (
								<button
									className="block w-full px-4 py-2 text-center text-caption font-medium text-muted-foreground hover:text-foreground disabled:opacity-50"
									disabled={remoteLoadingEarlierHost !== null}
									key={host.hostId}
									onClick={() => void loadRemoteEarlier(host.hostId, cursor)}
									type="button"
								>
									{remoteLoadingEarlierHost === host.hostId ? t("notify.loadingEarlier") : t("notify.loadEarlierFromHost", { host: host.label })}
								</button>
							);
						})}
						{remoteEarlierError ? <div aria-live="polite" className="px-4 py-2 text-caption text-error">{remoteEarlierError}</div> : null}
					</div>
						</>
				)}
						</>
					)}
				</NotificationWorkspaceState>
		</>
	);
	return <div className="flex h-full min-h-0 flex-col [&_[role=list]]:max-h-none [&_[role=list]]:min-h-0 [&_[role=list]]:flex-1">{body}</div>;
}

export { NotificationRuntime } from "./inbox/NotificationRuntime";
export { notificationCopy } from "./inbox/notification-copy";
