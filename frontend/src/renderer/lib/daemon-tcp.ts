import { useEffect, useState } from "react";
import { getApiBaseUrl } from "./api-client";
import { tallpineBridge } from "./bridge";

/**
 * Whether a URL is served by the local daemon (its API host or a preview
 * host, on its port). With loopback TCP off (B176) such a URL works inside
 * the app but is a dead link in the system browser (B180).
 */
export function isLocalDaemonUrl(rawUrl: string, apiBase: string = getApiBaseUrl()): boolean {
	try {
		const base = new URL(apiBase);
		const url = new URL(rawUrl);
		if (url.protocol !== "http:" || url.port !== base.port) return false;
		const host = url.hostname.toLowerCase();
		return host === "127.0.0.1" || host === "localhost" || host === "[::1]" || host.endsWith(".localhost");
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
