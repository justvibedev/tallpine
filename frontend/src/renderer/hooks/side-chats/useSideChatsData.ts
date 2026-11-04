/* Side-chat list, active-tab and snapshot polling, split out of chat/IndependentSideChats.tsx (B298, P56). */

import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type MutableRefObject } from "react";
import { apiErrorMessage, getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { openEventStream } from "../../lib/event-stream-socket";
import { getSideSnapshot, listSideChats, sideEventsUrl } from "../../lib/side-chats-api";
import { mergeLatestSnapshot, selectActiveAfterRefresh, type Side, type Snapshot } from "../../lib/side-chats-model";

export function useSideChatsData({
	sessionId,
	enabled,
	sessionRef,
	closedSidesRef,
	setError,
}: {
	sessionId: string;
	enabled: boolean;
	sessionRef: MutableRefObject<string>;
	closedSidesRef: MutableRefObject<Set<string>>;
	setError: (message: string | undefined) => void;
}) {
	const [sides, setSides] = useState<Side[]>([]);
	const [activeId, setActiveId] = useState<string>();
	const [visible, setVisible] = useState(false);
	const [snapshot, setSnapshot] = useState<Snapshot>();
	const [snapshotRefreshKey, setSnapshotRefreshKey] = useState(0);
	const [olderPages, setOlderPages] = useState<Snapshot[]>([]);
	const [loadingOlder, setLoadingOlder] = useState(false);
	const loadingOlderRef = useRef(false);
	const olderGeneration = useRef(0);
	const olderScope = useRef("");
	const nextOlderScope = JSON.stringify([sessionId, activeId, enabled]);
	if (olderScope.current !== nextOlderScope) {
		olderScope.current = nextOlderScope;
		olderGeneration.current++;
		loadingOlderRef.current = false;
	}
	useEffect(() => setLoadingOlder(false), [sessionId, activeId, enabled]);
	const baseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	useEffect(() => {
		closedSidesRef.current.clear();
		setSides([]);
		setActiveId(undefined);
		setVisible(false);
		setSnapshot(undefined);
		setOlderPages([]);
	}, [sessionId, enabled]);

	const refreshList = useCallback(async () => {
		if (!enabled) return;
		const { data, error: requestError } = await listSideChats(sessionId);
		if (sessionRef.current !== sessionId) return;
		if (requestError) {
			setError(apiErrorMessage(requestError));
			return;
		}
		const nextSides = Array.isArray(data?.sides) ? data.sides : [];
		setSides(nextSides);
		setActiveId((current) => selectActiveAfterRefresh(current, nextSides));
	}, [sessionId, enabled]);

	useEffect(() => {
		if (!enabled) return;
		const refresh = () => void refreshList().catch((error) => setError(String(error)));
		refresh();
		const timer = window.setInterval(refresh, 2000);
		return () => window.clearInterval(timer);
	}, [refreshList, enabled]);

	useEffect(() => {
		setOlderPages([]);
		loadingOlderRef.current = false;
		if (!enabled || !activeId) {
			setSnapshot(undefined);
			return;
		}
		let alive = true;
		let lastApplied = 0;
		let issued = 0;
		const refresh = async () => {
			const requestNumber = ++issued;
			const { data, error: requestError } = await getSideSnapshot(sessionId, activeId);
			if (!alive || requestNumber < lastApplied) return;
			lastApplied = requestNumber;
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			if (data?.snapshot) {
				const next = data.snapshot;
				setSnapshot((current) => mergeLatestSnapshot(current, next));
				setError(undefined);
			}
		};
		const refreshSafely = () =>
			void refresh().catch((error) => {
				if (alive) setError(String(error));
			});
		refreshSafely();
		let stream: EventSource | undefined;
		if (baseUrl && typeof EventSource !== "undefined") {
			stream = openEventStream(sideEventsUrl(baseUrl, sessionId, activeId));
			stream.addEventListener("changed", refreshSafely);
			stream.addEventListener("snapshot", refreshSafely);
		}
		const timer = window.setInterval(refreshSafely, stream ? 3000 : 500);
		return () => {
			alive = false;
			window.clearInterval(timer);
			stream?.close();
		};
	}, [sessionId, activeId, baseUrl, snapshotRefreshKey, enabled]);

	const loadOlder = useCallback(async () => {
		if (!activeId || !snapshot || loadingOlderRef.current) return;
		const oldest = olderPages.at(-1)?.turns?.[0]?.createdAt ?? snapshot.turns?.[0]?.createdAt;
		if (!oldest) return;
		loadingOlderRef.current = true;
		setLoadingOlder(true);
		const generation = ++olderGeneration.current;
		const scope = olderScope.current;
		const current = () =>
			olderGeneration.current === generation && olderScope.current === scope && !closedSidesRef.current.has(activeId);
		try {
			const { data, error: requestError } = await getSideSnapshot(sessionId, activeId, { before: oldest, limit: 50 });
			if (!current()) return;
			if (requestError) {
				setError(apiErrorMessage(requestError));
				return;
			}
			if (data?.snapshot && current()) setOlderPages((pages) => [...pages, data.snapshot]);
		} catch (cause) {
			if (current()) setError(apiErrorMessage(cause));
		} finally {
			if (current()) {
				loadingOlderRef.current = false;
				setLoadingOlder(false);
			}
		}
	}, [sessionId, activeId, snapshot, olderPages]);
	return {
		sides,
		setSides,
		activeId,
		setActiveId,
		visible,
		setVisible,
		snapshot,
		setSnapshot,
		setSnapshotRefreshKey,
		olderPages,
		loadingOlder,
		loadOlder,
		refreshList,
	};
}
