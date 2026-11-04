import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { baseUrlForHost, connectedHosts } from "../host-clients";
import { probeRemoteSse } from "../remote-sse-probe";
import { refreshRemoteConversation } from "../../hooks/conversation";
import { CDC_EVENT_TYPES, WORKSPACE_REFETCH_DEBOUNCE_MS, WORKSPACE_REFETCH_MAX_WAIT_MS } from "./constants";

export type RemoteHosts = {
	sync: () => void;
	dispose: () => void;
};

type RemoteConnection = {
	base: string;
	close?: () => void;
	pollTimer?: ReturnType<typeof setInterval>;
};

type RemoteHostsDeps = {
	queryClient: QueryClient;
	invalidate: (queryKey: QueryKey) => void;
	isDisposed: () => boolean;
};

// Mirrors each connected remote host's CDC stream (with a 2 s poll fallback)
// into the host-scoped query caches.
export function createRemoteHosts({ queryClient, invalidate, isDisposed }: RemoteHostsDeps): RemoteHosts {
	const remoteSources = new Map<string, RemoteConnection>();
	const remoteConversationRefreshes = new Map<string, { dirty: boolean }>();
	const refreshRemoteConversationOnce = (hostId: string, sessionId: string) => {
		const key = `${hostId}\0${sessionId}`;
		const running = remoteConversationRefreshes.get(key);
		if (running) { running.dirty = true; return; }
		const state = { dirty: false };
		remoteConversationRefreshes.set(key, state);
		void (async () => {
			do {
				state.dirty = false;
				try { await refreshRemoteConversation(queryClient, sessionId, hostId); }
				catch { /* A later event or the polling fallback can retry. */ }
			} while (state.dirty && !isDisposed() && baseUrlForHost(hostId));
			remoteConversationRefreshes.delete(key);
		})();
	};
	// The same trailing debounce for a remote host's list: its event stream
	// and 2 s poll would otherwise refetch it on every event.
	const remoteRefetches = new Map<string, { timer: ReturnType<typeof setTimeout>; firstAt: number }>();
	const requestRemoteWorkspaceRefetch = (hostId: string) => {
		if (isDisposed()) return;
		const now = Date.now();
		const pending = remoteRefetches.get(hostId);
		const firstAt = pending?.firstAt ?? now;
		if (pending) clearTimeout(pending.timer);
		const wait = Math.max(0, Math.min(WORKSPACE_REFETCH_DEBOUNCE_MS, firstAt + WORKSPACE_REFETCH_MAX_WAIT_MS - now));
		remoteRefetches.set(hostId, {
			firstAt,
			timer: setTimeout(() => {
				remoteRefetches.delete(hostId);
				invalidate(["remote-workspaces", hostId]);
			}, wait),
		});
	};
	const refreshRemote = (hostId: string, reconnect = false, immediate = false) => {
		if (reconnect || immediate) invalidate(["remote-workspaces", hostId]);
		else requestRemoteWorkspaceRefetch(hostId);
		invalidate(["project", hostId]);
		invalidate(["project-config", hostId]);
		if (reconnect) invalidate(["remote-conversation", hostId]);
		if (reconnect) invalidate(["reviewer-conversation", hostId]);
		invalidate(["session-scm-summary", hostId]);
		invalidate(["session-reviews", hostId]);
		invalidate(["session-usage", hostId]);
		invalidate(["session-usage", "detail", hostId]);
		invalidate(["session-interface-transition", hostId]);
		invalidate(["agent-readiness", hostId]);
		invalidate(["agent-models", hostId]);
	};
	const onRemoteEvent = (hostId: string, event: Event) => {
		refreshRemote(hostId);
		if (!("data" in event)) return;
		try {
			const decoded = JSON.parse(String((event as MessageEvent).data)) as { sessionId?: unknown; payload?: { conversationId?: unknown; reviewId?: unknown } };
			if (typeof decoded.sessionId === "string" && typeof decoded.payload?.conversationId === "string" && typeof decoded.payload.reviewId !== "string") {
				refreshRemoteConversationOnce(hostId, decoded.sessionId);
			}
			if (typeof decoded.payload?.reviewId === "string") {
				invalidate(["reviewer-conversation", hostId, decoded.payload.reviewId]);
			}
		} catch {
			// The host's project/session cache still refreshes after a malformed event.
		}
	};
	const connectRemote = (hostId: string) => {
		if (isDisposed()) return;
		const base = baseUrlForHost(hostId);
		if (!base) return;
		if (remoteSources.get(hostId)?.base === base) return;
		const connection: RemoteConnection = { base };
		remoteSources.set(hostId, connection);
		refreshRemote(hostId, true);
		connection.pollTimer = setInterval(() => {
			refreshRemote(hostId, false, true);
			invalidate(["reviewer-conversation", hostId]);
		}, 2_000);
		connection.close = probeRemoteSse(
			`${base.replace(/\/+$/, "")}/api/v1/events?after=latest`,
			CDC_EVENT_TYPES,
			(event) => {
				if (isDisposed() || remoteSources.get(hostId) !== connection) return;
				onRemoteEvent(hostId, event);
			},
			() => {
				if (remoteSources.get(hostId) !== connection) return;
				if (connection.pollTimer !== undefined) clearInterval(connection.pollTimer);
				connection.pollTimer = undefined;
				refreshRemote(hostId, true);
			},
			() => {
				if (remoteSources.get(hostId) !== connection) return;
				if (connection.pollTimer === undefined) connection.pollTimer = setInterval(() => {
					refreshRemote(hostId, false, true);
					invalidate(["reviewer-conversation", hostId]);
				}, 2_000);
				refreshRemote(hostId, false, true);
			},
		);
	};
	const sync = () => {
		const active = new Set(connectedHosts());
		for (const [hostId, connection] of remoteSources) {
			if (active.has(hostId) && connection.base === baseUrlForHost(hostId)) continue;
			connection.close?.();
			if (connection.pollTimer !== undefined) clearInterval(connection.pollTimer);
			remoteSources.delete(hostId);
		}
		for (const hostId of active) connectRemote(hostId);
	};
	const dispose = () => {
		for (const pending of remoteRefetches.values()) clearTimeout(pending.timer);
		remoteRefetches.clear();
		for (const connection of remoteSources.values()) {
			connection.close?.();
			if (connection.pollTimer !== undefined) clearInterval(connection.pollTimer);
		}
		remoteSources.clear();
	};
	return { sync, dispose };
}
