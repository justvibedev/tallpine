import { fetchFor } from "./remote-tls";
import type { RemoteEntry } from "./remotes-store";

/** Prefix of the per-device tokens a host issues in exchange for its pairing
 * code. A saved password without it is a pairing code or a pre-token password. */
export const DEVICE_TOKEN_PREFIX = "vot_";

type FetchImpl = typeof fetch;

/** Trades what the user typed (the code shown in Connect Mobile) or a saved
 * pre-token password for a device token; resolves to what to store. */
export type PasswordExchange = (
	entry: Pick<RemoteEntry, "url" | "password" | "cert">,
	installId: string,
) => Promise<string>;

/** Used where no exchange is wired (tests): the password is stored as given. */
export const keepPassword: PasswordExchange = async (entry) => entry.password;

/**
 * Sends the password both as the pairing code and as the legacy bearer, so one
 * request covers a freshly added host (code) and a host saved before device
 * tokens existed (old password, honoured by the host during its grace window).
 *
 * Falls back to the password unchanged when the host predates the route (404/405),
 * refuses it, or does not answer: the caller's probe then reports the real
 * state instead of this function inventing a second failure mode.
 */
export function createPasswordExchange(fetchImpl: FetchImpl | undefined, deviceName: () => Promise<string>): PasswordExchange {
	return async (entry, installId) => {
		if (!entry.password || entry.password.startsWith(DEVICE_TOKEN_PREFIX)) return entry.password;
		try {
			const base = (entry.url.includes("://") ? entry.url : `http://${entry.url}`).replace(/\/+$/, "");
			const url = new URL(`${base}/pair/exchange`);
			if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return entry.password;
			const response = await (fetchImpl ?? fetchFor(entry.cert))(url.href, {
				method: "POST",
				redirect: "error",
				headers: { "Content-Type": "application/json", Authorization: `Bearer ${entry.password}` },
				body: JSON.stringify({ code: entry.password, installId, deviceName: await deviceName() }),
				signal: AbortSignal.timeout(8_000),
			});
			if (!response.ok) return entry.password;
			const body = (await response.json()) as { token?: unknown };
			return typeof body.token === "string" && body.token.startsWith(DEVICE_TOKEN_PREFIX) ? body.token : entry.password;
		} catch {
			return entry.password;
		}
	};
}
