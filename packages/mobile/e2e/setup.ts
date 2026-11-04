import { createHash } from "node:crypto";
import type { TLSSocket } from "node:tls";
import https from "node:https";

/**
 * Stands in for the two native pieces the phone gets from React Native:
 *  - the TallpinePinning module (lib/pinning.ts hands it `host:port=pin` pairs)
 *  - a fetch that enforces those pins on https
 * Everything above them (pairing, connect race, api, chat) is the shipped code.
 */
const pins = new Map<string, string>();

(globalThis as { expo?: unknown }).expo = {
	modules: {
		TallpinePinning: {
			setPins: (pairs: string[]) => {
				for (const pair of pairs) {
					const eq = pair.lastIndexOf("=");
					pins.set(pair.slice(0, eq), pair.slice(eq + 1));
				}
			},
			probe: async () => ({ pin: "", trusted: false }),
		},
	},
};

/** SHA-256 of the peer certificate's SubjectPublicKeyInfo, lowercase hex. */
function peerPin(socket: TLSSocket): string | undefined {
	const cert = socket.getPeerX509Certificate();
	if (!cert) return undefined; // torn down already: fail closed
	return createHash("sha256").update(cert.publicKey.export({ type: "spki", format: "der" })).digest("hex");
}

const nodeFetch = globalThis.fetch.bind(globalThis);

function pinnedFetch(input: string, init: RequestInit): Promise<Response> {
	const url = new URL(input);
	const expected = pins.get(`${url.hostname.toLowerCase()}:${url.port || 443}`);
	return new Promise((resolve, reject) => {
		const req = https.request(
			url,
			{
				agent: false, // one socket per request, so each connection gets its pin check
				method: init.method ?? "GET",
				headers: init.headers as Record<string, string>,
				// Without a pin the host must chain to a CA, like the OS default.
				rejectUnauthorized: !expected,
			},
			(res) => {
				const chunks: Buffer[] = [];
				res.on("data", (c: Buffer) => chunks.push(c));
				res.on("end", () => {
					const headers = new Headers();
					for (const [k, v] of Object.entries(res.headers)) if (v !== undefined) headers.set(k, Array.isArray(v) ? v.join(", ") : v);
					const status = res.statusCode ?? 0;
					const body = [204, 205, 304].includes(status) ? null : Buffer.concat(chunks);
					resolve(new Response(body, { status, statusText: res.statusMessage ?? "", headers }));
				});
			},
		);
		req.on("error", reject);
		// Node skips checkServerIdentity for a self-signed chain, so the pin is
		// checked here, before any request byte is written.
		if (expected) {
			req.on("socket", (socket) => {
				socket.once("secureConnect", () => {
					if (peerPin(socket as TLSSocket) !== expected) req.destroy(new Error(`pin mismatch for ${url.host}`));
				});
			});
		}
		const signal = init.signal;
		if (signal) {
			if (signal.aborted) req.destroy(Object.assign(new Error("aborted"), { name: "AbortError" }));
			signal.addEventListener("abort", () => req.destroy(Object.assign(new Error("aborted"), { name: "AbortError" })), { once: true });
		}
		req.end(typeof init.body === "string" ? init.body : undefined);
	});
}

globalThis.fetch = ((input: Parameters<typeof fetch>[0], init?: RequestInit) => {
	const raw = typeof input === "string" ? input : input instanceof URL ? input.href : (input as Request).url;
	return raw.startsWith("https:") ? pinnedFetch(raw, init ?? {}) : nodeFetch(input, init);
}) as typeof fetch;
