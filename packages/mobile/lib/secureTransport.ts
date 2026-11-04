import { HTTPS_REQUIRED } from "./authCodes";

/** Title for a pairing that must be re-scanned to get a secure connection. */
export const HTTPS_REQUIRED_TITLE = "Re-pair for a secure connection";

/** Why a pairing on a plain address stopped working, and how to fix it. */
export const HTTPS_REQUIRED_MESSAGE = "This phone was paired before the connection was secured. Show a new code on that computer and scan it once.";

/**
 * Thrown instead of building a plain http or ws URL. The phone never talks to a
 * computer over plain http, on any network: the device token travels in every
 * request. It carries the same status and code the desktop answers a plain
 * request with (426 HTTPS_REQUIRED), so a pairing saved by an older build that
 * still names a plain address lands on the "Re-pair for a secure connection"
 * screen instead of failing silently.
 */
export class InsecureTransportError extends Error {
	readonly status = 426;
	readonly code = HTTPS_REQUIRED;
	constructor(host: string, port: number | string) {
		super(`${host}:${port} is not a secure address. Show a new code on your computer and scan it once.`);
		this.name = "InsecureTransportError";
	}
}

/**
 * The only way the app builds a base URL for a computer: `https://host:port`
 * (or `wss://` for the terminal mux). A target that is not marked secure, so
 * neither pinned by a pairing nor validated by a trusted certificate, throws.
 */
export function secureOrigin(target: { host: string; port: number | string; secure?: boolean }, scheme: "https" | "wss" = "https"): string {
	if (!target.secure) throw new InsecureTransportError(target.host, target.port);
	return `${scheme}://${target.host}:${target.port}`;
}
