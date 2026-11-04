import { secureOrigin } from "./secureTransport";

/**
 * One advertised way to reach a paired machine. The daemon lists every one it
 * knows about and the phone races them, so a machine on both Wi-Fi and Ethernet
 * offers both and either can win.
 *
 * Mirrors MobileEndpoint in the daemon's OpenAPI schema.
 */
export type EndpointKind = "lan" | "tailscale" | "tunnel" | "relay";

export type Endpoint = {
	kind: EndpointKind;
	host: string;
	port: number;
	secure: boolean;
	/**
	 * SHA-256 of the gateway certificate's public key (lowercase hex). When
	 * present the phone connects over https and accepts only that key, and
	 * `secure` is true (see normalizeEndpoint).
	 */
	pin?: string;
};

/** Keep a pasted URL from becoming the endpoint's DNS hostname. */
export function normalizeServerHost(host: string): string {
	return host
		.trim()
		.replace(/^[a-z][a-z0-9+.-]*:\/\//i, "")
		.replace(/\/+$/, "");
}

/**
 * Preference when several endpoints answer together, cheapest first.
 *
 * Measured against a real tunnel: LAN round-trips in ~1ms at 328MB/s, the
 * tunnel in 11-65ms at 4.4MB/s. Tailscale sits between them — direct, but
 * usually through a relay or a longer path than the local segment.
 */
const KIND_PREFERENCE: readonly EndpointKind[] = ["lan", "tailscale", "tunnel", "relay"];

/** Lower is better. Unknown kinds sort last rather than throwing, so a newer
 * daemon advertising a kind this build does not know about still works. */
export function endpointRank(kind: EndpointKind): number {
	const i = KIND_PREFERENCE.indexOf(kind);
	return i === -1 ? KIND_PREFERENCE.length : i;
}

/** Stable identity for an endpoint, for dedupe and health bookkeeping. */
export function endpointKey(e: Endpoint): string {
	return `${e.kind}://${e.host}:${e.port}`;
}

/** The base URL to talk to this endpoint on; throws InsecureTransportError for a plain one. */
export function endpointBaseUrl(e: Endpoint): string {
	return secureOrigin(e);
}

/** The endpoint's address as shown to the user; never used to connect. */
export function endpointDisplayUrl(e: Endpoint): string {
	return `${e.secure ? "https" : "http"}://${e.host}:${e.port}`;
}
