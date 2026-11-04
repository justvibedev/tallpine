/* Split out of ConnectMobileContent.tsx (B298, P55). */

import { scramblePairingCodes } from "./qrScramble";
import {
buildPairingOffer,
pairingCodeUrl,
type PairingEndpoint,
} from "./pairing-payload";


// One scan gives the mobile app every value required to connect. Keep the
// secure key absent for plaintext payloads so older mobile builds can decode
// the same bytes they already understand.
export function pairingPayload(host: string, port: number, password: string, secure?: boolean): string {
	return JSON.stringify(secure ? { v: 1, host, port, password, secure: true } : { v: 1, host, port, password });
}

/**
 * The value encoded into the pairing QR.
 *
 * Prefers the v2 deep link, which carries every endpoint the daemon advertises
 * so the phone can race them, and opens the app straight from the system
 * camera. The payload rides in the fragment, so the connection token never
 * reaches a server even when the link is opened in a browser.
 *
 * Falls back to the v1 payload when the daemon advertises no endpoints — an
 * older daemon, or one whose network is not up yet. Showing a dead v2 QR there
 * would be worse than the pairing that already works.
 */
export function qrValueFor(input: {
	hostId: string;
	host: string;
	platform: string;
	password: string;
	endpoints: readonly PairingEndpoint[];
}): string {
	// v2 only. The phone no longer accepts v1: those codes predate the identity
	// probe the race uses to verify a machine before sending it a credential, so
	// a daemon old enough to need one could never complete a race anyway. An
	// empty endpoint list means there is nothing to advertise yet, and the panel
	// shows the preparing state rather than a code — see qrIsReady.
	return pairingCodeUrl(
		buildPairingOffer({
			endpoints: input.endpoints,
			password: input.password,
			hostId: input.hostId,
			name: input.host,
			platform: input.platform,
		}),
		PAIRING_LINK_BASE,
	);
}

/**
 * How often to re-read mobile status, or false to stop.
 *
 * The connector takes roughly half a minute to become advertisable, and the
 * status query is otherwise fetched once on open and refetched only after a
 * mutation. Without this the modal would sit on "Preparing remote access…"
 * indefinitely while the daemon had long since finished.
 *
 * Polls only while there is a transient state to wait out, so an idle modal
 * does not keep hitting the daemon for the rest of the session.
 */
export type PairingReadinessStatus = {
	enabled: boolean;
	noPublicTunnel?: boolean;
	endpoints?: readonly PairingEndpoint[];
	tunnel?: {
		supported?: boolean;
		running: boolean;
		ready: boolean;
		lastError?: string;
		[k: string]: unknown;
	};
};

export function pairingIsPending(status: PairingReadinessStatus | undefined): boolean {
	if (!status?.enabled) return false;
	if (!status.endpoints || status.endpoints.length === 0) return true;
	if (status.noPublicTunnel === true) return false;
	const tunnel = status.tunnel;
	if (!tunnel || tunnel.ready || tunnel.lastError) return false;
	return tunnel.supported === true || tunnel.running;
}

export function mobileStatusRefetchInterval(status: PairingReadinessStatus | undefined): number | false {
	return pairingIsPending(status) ? MOBILE_STATUS_POLL_MS : false;
}

export const MOBILE_STATUS_POLL_MS = 2_000;

/**
 * Whether the pairing QR is safe to show.
 *
 * Pairing can start as soon as any endpoint is reachable. Remote access is
 * optional and can take up to a minute to start, so it must not block a LAN
 * code. The status query continues polling while the tunnel starts and the QR
 * is refreshed with the remote endpoint once the daemon advertises it.
 *
 * A daemon that does not report endpoints at all predates the endpoint race.
 * It has no tunnel to wait for, and its QR still works, so it is shown — the
 * absence of the field is not the same as an empty list.
 */
/** Every address a phone can use to reach this machine (LAN, MagicDNS, Tailscale IP);
 *  the first-listed local one when the daemon advertises none. */
export function pairingAddressLines(endpoints: readonly PairingEndpoint[] | undefined, fallback: string): string[] {
	const lines = (endpoints ?? []).filter((e) => e.kind === "lan" || e.kind === "tailscale").map((e) => `${e.host}:${e.port}`);
	return lines.length > 0 ? [...new Set(lines)] : [fallback];
}

export function qrIsReady(status: PairingReadinessStatus): boolean {
	if (!status.enabled) return false;
	// Nothing to encode: a v2 code carries the endpoint list, and there is no
	// longer a v1 form to fall back to. An absent list is as unready as an empty
	// one — it means the daemon has not told us where it can be reached.
	if (!status.endpoints || status.endpoints.length === 0) return false;
	return true;
}

/**
 * Why the code on screen has no public tunnel, or null when it has one (or
 * when there is no code to judge). Without the tunnel the code reaches this
 * machine from elsewhere only over Tailscale, if it carries a tailnet address.
 *
 * qrIsReady shows a LAN code while the tunnel is still starting, and the
 * phone races only what the code carries. A code scanned or photographed in
 * that window looks identical to a complete one, then fails away from home
 * with "is your phone on the same Wi-Fi?" — so the panel has to say so.
 * cloudflared being absent is stated separately (remoteAccessUnavailable), and
 * opting out of the tunnel is the user's own choice, so neither is reported.
 */
export function localOnlyReason(status: PairingReadinessStatus): "starting" | "failed" | null {
	if (!status.enabled || !status.endpoints || status.endpoints.length === 0) return null;
	if (status.endpoints.some((e) => e.kind === "tunnel")) return null;
	if (status.noPublicTunnel === true || status.tunnel?.supported === false) return null;
	if (pairingIsPending(status)) return "starting";
	if (status.tunnel?.lastError) return "failed";
	return null;
}

/** The app's registered scheme (app.json `expo.scheme`), not a universal link:
 * it works today, with no association files to host and no store listing
 * required. Universal links can be added later without changing the payload. */
export const PAIRING_LINK_BASE = "tallpine-mobile://pair";

/** A decoy with the same payload shape as a real pairing offer, drawn at the
 * same pinned QR version, so the blurred preview looks like the code it
 * becomes. */
export function placeholderQrValue(status?: { hostId?: string; host?: string; password?: string }): string {
	return scramblePairingCodes(1, {
		...(status?.hostId ? { hostIdLength: status.hostId.length } : {}),
		...(status?.host ? { nameLength: status.host.length } : {}),
		...(status?.password ? { tokenLength: status.password.length } : {}),
	})[0];
}
