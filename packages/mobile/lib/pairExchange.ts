import type { Endpoint } from "./endpoints";
import { endpointBaseUrl } from "./endpoints";

/** Prefix of the per-device tokens the daemon issues. A stored token without
 * it is the pre-token connection password, which the daemon honours only for a
 * grace window while the phone upgrades it. */
export const DEVICE_TOKEN_PREFIX = "vot_";

export function isDeviceToken(token: string): boolean {
	return token.startsWith(DEVICE_TOKEN_PREFIX);
}

export type ExchangeResult =
	| { ok: true; token: string }
	/** status is undefined when the daemon never answered; 404/405 means a
	 * daemon too old to know the exchange route. */
	| { ok: false; status?: number; /** The daemon's envelope code, when it sent one. */ code?: string };

export type ExchangeCredential = { code: string } | { legacyBearer: string };

export type ExchangeDeps = {
	fetch: (url: string, init: { method: string; headers: Record<string, string>; body: string; signal?: AbortSignal }) => Promise<{ ok: boolean; status: number; json: () => Promise<unknown> }>;
	installId: () => Promise<string>;
	deviceName?: string;
};

/**
 * Trades the single-use pairing code (or, for a phone paired before device
 * tokens, its old password) for a long random per-device token.
 *
 * Never throws: a failure is reported so the caller can fall back to the old
 * flow against a daemon that predates the route.
 */
export async function exchangeForDeviceToken(endpoint: Endpoint, credential: ExchangeCredential, deps: ExchangeDeps): Promise<ExchangeResult> {
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), 8_000);
	try {
		const headers: Record<string, string> = { "Content-Type": "application/json" };
		if ("legacyBearer" in credential) headers.Authorization = `Bearer ${credential.legacyBearer}`;
		const res = await deps.fetch(`${endpointBaseUrl(endpoint)}/pair/exchange`, {
			method: "POST",
			headers,
			body: JSON.stringify({
				...("code" in credential ? { code: credential.code } : {}),
				installId: await deps.installId(),
				...(deps.deviceName ? { deviceName: deps.deviceName } : {}),
			}),
			signal: controller.signal,
		});
		if (!res.ok) {
			let code: string | undefined;
			try {
				const body = (await res.json()) as { code?: unknown };
				if (typeof body.code === "string") code = body.code;
			} catch {
				/* not the envelope */
			}
			return { ok: false, status: res.status, ...(code ? { code } : {}) };
		}
		const body = (await res.json()) as { token?: unknown };
		return typeof body.token === "string" && isDeviceToken(body.token) ? { ok: true, token: body.token } : { ok: false, status: res.status };
	} catch {
		return { ok: false };
	} finally {
		clearTimeout(timer);
	}
}
