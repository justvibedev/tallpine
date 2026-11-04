import type { QueryClient } from "@tanstack/react-query";
import { openEventStream } from "../event-stream-socket";
import { getApiBaseUrl, hasTrustedApiBaseUrl } from "../api-client";
import { setEventsConnectionState } from "../events-connection";
import { computeSseRetryDelayMs } from "../sse-backoff";
import { workspaceQueryKey } from "../../hooks/useWorkspaceQuery";
import { codexAccountsQueryKey, writeCodexAccounts } from "../../hooks/codex-accounts-state";
import type { components } from "../../../api/schema";
import { CDC_EVENT_TYPES, EVENTSOURCE_CLOSED, STATUS_READINESS_EVENT } from "./constants";

export type SourceConnector = {
	connectSource: () => void;
	// Bumps the health attempt so an in-flight health check is ignored.
	invalidateHealth: () => void;
	dispose: () => void;
};

type SourceConnectorDeps = {
	queryClient: QueryClient;
	refreshWorkspaces: (event?: Event) => void;
	isDisposed: () => boolean;
};

// Owns the CDC and Codex-account event streams: opens them against the
// current daemon base URL, rebinds on change and paces retries.
export function createSourceConnector({ queryClient, refreshWorkspaces, isDisposed }: SourceConnectorDeps): SourceConnector {
	let healthAttempt = 0;
	let retryTimer: ReturnType<typeof setTimeout> | undefined;
	let source: EventSource | undefined;
	let sourceBaseUrl: string | undefined;
	// Base URL of a daemon that rejected ?after=latest (built before the cursor
	// existed). Its stream connects without one, replaying history as before.
	let cursorUnsupportedBaseUrl: string | undefined;
	const probeCursorSupport = (baseUrl: string, url: string) => {
		const controller = new AbortController();
		void fetch(url, { signal: controller.signal })
			.then((response) => {
				if (response.status === 400) cursorUnsupportedBaseUrl = baseUrl;
			})
			.catch(() => undefined)
			// A supporting daemon answers with an open stream; do not keep it.
			.finally(() => controller.abort());
	};
	let accountSource: EventSource | undefined;
	let accountSourceBaseUrl: string | undefined;
	const applyAccountEvent = (event: Event) => {
		if (isDisposed() || !("data" in event)) return;
		try {
			const decoded = JSON.parse(String((event as MessageEvent).data)) as components["schemas"]["CodexAccountsResponse"];
			writeCodexAccounts(queryClient, decoded, "replace");
		} catch {
			// A malformed transient event cannot replace the cached safe snapshot.
		}
	};
	// Consecutive scheduled rebuilds since the stream last opened. Paces
	// the retry so a daemon that keeps refusing the stream is not
	// hammered on a flat cadence (#4323).
	let retries = 0;

	const scheduleRetry = () => {
		if (isDisposed() || retryTimer) return;
		retries += 1;
		retryTimer = setTimeout(() => {
			retryTimer = undefined;
			connectSource();
		}, computeSseRetryDelayMs(retries));
	};

	const connectAccountSource = (baseUrl: string) => {
		if (accountSource && accountSourceBaseUrl === baseUrl && accountSource.readyState !== EVENTSOURCE_CLOSED) return;
		accountSource?.close();
		accountSourceBaseUrl = baseUrl;
		try {
			accountSource = openEventStream(`${baseUrl.replace(/\/+$/, "")}/api/v1/agents/codex/accounts/events`);
			accountSource.onopen = () => {
				if (isDisposed()) return;
				void queryClient.invalidateQueries({ queryKey: codexAccountsQueryKey });
			};
			accountSource.onerror = () => { if (accountSource?.readyState === EVENTSOURCE_CLOSED) scheduleRetry(); };
			accountSource.addEventListener("codex_account", applyAccountEvent);
		} catch {
			accountSource = undefined;
		}
	};

	const openCdcSource = (baseUrl: string) => {
		try {
			// Start at the newest event. Without a cursor the daemon replays the
			// whole change_log (up to 100k rows under retention) on every connect,
			// although onopen already refreshes everything below. The daemon then
			// sends a cursor id, so the browser's own reconnect replays only the gap.
			// A daemon from before the cursor existed answers 400; that base URL
			// falls back to the cursorless stream (see probeCursorSupport).
			const streamUrl = `${baseUrl.replace(/\/+$/, "")}/api/v1/events`;
			const useCursor = cursorUnsupportedBaseUrl !== baseUrl;
			source = openEventStream(useCursor ? `${streamUrl}?after=latest` : streamUrl);
			const connectedSource = source;
			let opened = false;
			source.onopen = () => {
				if (isDisposed() || source !== connectedSource) return;
				opened = true;
				healthAttempt += 1;
				retries = 0;
				setEventsConnectionState("connected");
				// Events emitted during the gap were lost; refetch once on (re)open.
				refreshWorkspaces();
			};
			source.onerror = () => {
				if (isDisposed() || source !== connectedSource) return;
				// While readyState is CONNECTING the browser retries on its own;
				// either way the stream is not delivering, so surface it instead
				// of looping silently against a dead daemon.
				setEventsConnectionState("disconnected");
				if (source?.readyState === EVENTSOURCE_CLOSED) {
					// Closed before it ever opened: maybe this daemon rejects the cursor.
					if (!opened && useCursor) probeCursorSupport(baseUrl, `${streamUrl}?after=latest`);
					scheduleRetry();
				}
				const attempt = ++healthAttempt;
				void queryClient.refetchQueries(
					{ queryKey: workspaceQueryKey, type: "active" },
					{ throwOnError: true },
				).then(
					() => {
						if (attempt !== healthAttempt || source !== connectedSource) return;
					},
					() => {
						if (attempt !== healthAttempt || source !== connectedSource) return;
					},
				);
			};
			source.onmessage = refreshWorkspaces; // unnamed events, if any
			for (const type of CDC_EVENT_TYPES) {
				source.addEventListener(type, refreshWorkspaces);
			}
			// Live-only (not in change_log): a session finished its startup status check.
			source.addEventListener(STATUS_READINESS_EVENT, refreshWorkspaces);
			// EventSource auto-reconnects and resumes via Last-Event-ID while
			// CONNECTING; scheduleRetry only covers the terminal CLOSED state.
		} catch {
			source = undefined;
		}
	};

	const connectSource = () => {
		// EventSource is unavailable in jsdom (tests) and some preview surfaces; guard it.
		if (isDisposed() || typeof EventSource === "undefined") return;
		if (!hasTrustedApiBaseUrl()) {
			healthAttempt += 1;
			source?.close();
			accountSource?.close();
			source = undefined;
			accountSource = undefined;
			sourceBaseUrl = undefined;
			accountSourceBaseUrl = undefined;
			setEventsConnectionState("disconnected");
			return;
		}
		const baseUrl = getApiBaseUrl();
		connectAccountSource(baseUrl);
		// Keep a still-usable source on the same base URL; replace one the
		// browser abandoned (CLOSED) or one bound to a stale port.
		if (source && sourceBaseUrl === baseUrl && source.readyState !== EVENTSOURCE_CLOSED) return;
		// A daemon that came back on a different port is a fresh target, not
		// a continuation of the dead one: do not make it serve the delay the
		// old port earned.
		if (sourceBaseUrl && sourceBaseUrl !== baseUrl) retries = 0;
		source?.close();
		source = undefined;
		sourceBaseUrl = baseUrl;
		openCdcSource(baseUrl);
	};

	const dispose = () => {
		healthAttempt += 1;
		if (retryTimer) clearTimeout(retryTimer);
		source?.close();
		accountSource?.close();
	};
	return { connectSource, invalidateHealth: () => { healthAttempt += 1; }, dispose };
}
