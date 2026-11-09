import { useEffect, useState } from "react";
import { getApiBaseUrl } from "./api-client";
import { isLocalDaemonUrlOnPort } from "../../shared/daemon-url";
import { tallpineBridge } from "./bridge";

/** Whether a URL is served by the local daemon (see shared/daemon-url). */
export function isLocalDaemonUrl(rawUrl: string, apiBase: string = getApiBaseUrl()): boolean {
	try {
		return isLocalDaemonUrlOnPort(rawUrl, new URL(apiBase).port);
	} catch {
		return false;
	}
}

const CACHE_MS = 10_000;
let cache: { at: number; value: Promise<boolean> } | undefined;

/** Drops the cached probe (tests). */
export function resetDaemonTcpCache(): void {
	cache = undefined;
}

/** One probe shared by every caller for a few seconds, so mounting many render frames costs one IPC. */
function probeDaemonTcpOpen(): Promise<boolean> {
	const now = Date.now();
	if (!cache || now - cache.at > CACHE_MS) {
		cache = { at: now, value: tallpineBridge.app.daemonTcpOpen().catch(() => true) };
	}
	return cache.value;
}

/** Whether the daemon answers on loopback TCP; optimistic until probed. */
export function useDaemonTcpOpen(): boolean {
	const [open, setOpen] = useState(true);
	useEffect(() => {
		let live = true;
		void probeDaemonTcpOpen().then((value) => live && setOpen(value));
		return () => {
			live = false;
		};
	}, []);
	return open;
}
