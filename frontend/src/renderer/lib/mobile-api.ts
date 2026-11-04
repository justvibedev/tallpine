/* Split out of ConnectMobileContent.tsx (B298, P55). */

import { apiClient, apiErrorMessage } from "./api-client";
import type { PairingEndpoint } from "./pairing-payload";

export interface MobileStatus {
	/** Master switch. Off by default: with it off the daemon runs no mobile
	 * listener, pairing or push routes, push relay, or tunnel. */
	featureEnabled?: boolean;
	/** The user's reachable base URL that pairing codes advertise. */
	publicBaseUrl?: string;
	/** Whether publicBaseUrl is actually served. Only "ok" is advertised. */
	publicUrlState?: string;
	/** Explicit Cloudflare quick-tunnel opt-in. */
	quickTunnel?: boolean;
	enabled: boolean;
	/** User intent, independent of whether cloudflared is installed or running. */
	noPublicTunnel?: boolean;
	browserRemoteAccess?: boolean;
	/** Ends legacy (8-char) pairings after their 14-day grace; blocks older apps. */
	requireDevicePairing?: boolean;
	/** An old 8-character password still authenticates a phone (static bearer). */
	legacyPasswordActive?: boolean;
	/** End of the legacy-pairing grace (RFC 3339); absent when none is configured. */
	legacyUntil?: string;
	/** This machine's stable identity, echoed into the pairing code so the phone
	 * can verify the endpoints it races. Optional: a daemon predating the
	 * endpoint race does not send it, and the QR falls back to the v1 payload. */
	hostId?: string;
	/** Every advertised way to reach this daemon, in preference order. Optional
	 * for the same reason as hostId. */
	endpoints?: PairingEndpoint[];
	/** Managed remote-access connector state, for showing progress while the
	 * tunnel comes up. */
	tunnel?: {
		/** False when this machine has no connector at all — cloudflared is
		 * absent. Distinct from "not started yet", which is running:false. */
		supported?: boolean;
		running: boolean;
		ready: boolean;
		hostname: string;
		location: string;
		lastError: string;
	};
	host: string;
	tailscaleHost: string;
	port: number;
	password: string;
	warning: string;
	/** Gateway certificate fingerprint; non-empty means the phone connection is TLS-pinned. */
	tlsPin?: string;
	securePairing: {
		enabled: boolean;
		available: boolean;
		active: boolean;
		host: string;
		port: number;
		reason: string;
	};
	/** macOS-only option to hold the machine awake while the bridge is on.
	 * Optional: a daemon predating it does not send the block. */
	keepAwake?: {
		supported: boolean;
		enabled: boolean;
		active: boolean;
		hasBattery: boolean;
		lidClosed?: boolean;
		lidSupported?: boolean;
		lidActive?: boolean;
		lidReason?: string;
	};
}

export async function fetchMobileStatus(): Promise<MobileStatus> {
	const { data, error } = await apiClient.GET("/api/v1/mobile/status");
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

export type KeepAwakeStatus = NonNullable<MobileStatus["keepAwake"]>;

/**
 * Keep awake as last seen, for the one moment nothing better is known: a cold
 * open of this page before the status has loaded (Settings prefetches it, so
 * this is rare). The row is drawn disabled in this state rather than popping
 * in, or flipping, when the status lands.
 */
export const KEEP_AWAKE_STORAGE_KEY = "ao.mobile.keepAwake";

export function readLastKeepAwake(): KeepAwakeStatus | null {
	try {
		const raw = window.localStorage?.getItem(KEEP_AWAKE_STORAGE_KEY);
		if (!raw) return null;
		const saved = JSON.parse(raw) as Partial<KeepAwakeStatus> | null;
		if (typeof saved?.supported !== "boolean" || typeof saved.enabled !== "boolean") return null;
		return { supported: saved.supported, enabled: saved.enabled, active: false, hasBattery: saved.hasBattery === true };
	} catch {
		return null;
	}
}

export function rememberKeepAwake(keepAwake: KeepAwakeStatus | undefined) {
	try {
		if (!keepAwake) window.localStorage?.removeItem(KEEP_AWAKE_STORAGE_KEY);
		else
			window.localStorage?.setItem(
				KEEP_AWAKE_STORAGE_KEY,
				JSON.stringify({ supported: keepAwake.supported, enabled: keepAwake.enabled, hasBattery: keepAwake.hasBattery }),
			);
	} catch {
		// Storage can be unavailable; the row then waits for the status.
	}
}
