import { tallpineBridge } from "./bridge";
import type { QueryClient } from "@tanstack/react-query";
import { subscribeApiBaseUrl } from "./api-client";
import { setEventsConnectionState } from "./events-connection";
import { subscribeConnectedHosts } from "./host-clients";
import { createWorkspaceRefresher } from "./event-transport/cdc-event";
import { createInvalidator } from "./event-transport/invalidator";
import { createPendingState } from "./event-transport/pending-state";
import { createRefreshScheduler } from "./event-transport/refresh-scheduler";
import { createRemoteHosts } from "./event-transport/remote-hosts";
import { createSourceConnector } from "./event-transport/source-connector";

export type EventTransport = {
	connect: () => () => void;
};

/**
 * Wires live server state into the TanStack Query cache. Three sources feed it:
 *   - daemon lifecycle over Electron IPC (coming up/down changes session availability)
 *   - each connected daemon's CDC stream over SSE (project/session/PR changes)
 *   - the Codex account stream over SSE (account, capacity, and switch state)
 * Lifecycle and CDC events invalidate the owning host's cache; durable per-session
 * updates also refresh editor-handoff readiness. Invalidations are batched
 * because a single user action can emit a burst of CDC events.
 */
export function createEventTransport(queryClient: QueryClient): EventTransport {
	return {
		connect() {
			let disposed = false;
			const isDisposed = () => disposed;
			const pending = createPendingState();
			const invalidator = createInvalidator(queryClient, isDisposed);
			const scheduler = createRefreshScheduler({ queryClient, invalidate: invalidator.invalidate, pending, isDisposed });
			const refreshWorkspaces = createWorkspaceRefresher({ pending, scheduler, isDisposed });
			const remoteHosts = createRemoteHosts({ queryClient, invalidate: invalidator.invalidate, isDisposed });
			const sources = createSourceConnector({ queryClient, refreshWorkspaces, isDisposed });

			// Main re-pushes the same status on every getStatus poll (~10s) when it is
			// attached to a daemon it did not spawn. Only a real change (state, port or
			// pid) can mean missed events; refreshing on each echo refetched every open
			// conversation and the workspace list every 10 seconds.
			let lastDaemonStatusKey: string | undefined;
			const removeDaemonListener = tallpineBridge.daemon.onStatus((status) => {
				const key = `${status?.state}:${status?.port ?? ""}:${status?.pid ?? ""}`;
				if (key === lastDaemonStatusKey) return;
				lastDaemonStatusKey = key;
				sources.connectSource();
				refreshWorkspaces();
			});
			// Rebind when the daemon comes back on a different port, independent of
			// status-event ordering.
			const removeBaseUrlListener = subscribeApiBaseUrl(sources.connectSource);
			sources.connectSource();
			const removeConnectedHostsListener = subscribeConnectedHosts(remoteHosts.sync);
			remoteHosts.sync();

			return () => {
				sources.invalidateHealth();
				disposed = true;
				scheduler.dispose();
				invalidator.clear();
				removeDaemonListener();
				removeBaseUrlListener();
				removeConnectedHostsListener();
				sources.dispose();
				remoteHosts.dispose();
				setEventsConnectionState("idle");
			};
		},
	};
}
