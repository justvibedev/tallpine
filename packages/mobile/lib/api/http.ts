import { authHeaders, httpBase, type ServerConfig } from "../config";
import { isPairingRevoked, markPairingRevoked, PAIRING_REMOVED_MESSAGE } from "../revokedPairing";
import { cachedInstallId } from "../installId";
import { UnreachableError } from "../connectionError";
import { revokesPairing } from "../authCodes";
import { InsecureTransportError } from "../secureTransport";

export const API = "/api/v1";

// ---- Low-level fetch with friendly errors ----------------------------------

const REQUEST_TIMEOUT_MS = 12000;
export const DISCONNECT_REQUEST_TIMEOUT_MS = 2000;
// The daemon gives attachment uploads 10 minutes; allow time for its response.
// A cold agent resume spawns the chat host and reloads the transcript; the
// daemon allows the driver 60s for that and cancels it if the client gives up.
export const RESUME_REQUEST_TIMEOUT_MS = 70_000;
export const ATTACHMENT_REQUEST_TIMEOUT_MS = 11 * 60_000;

// The server answered, but with an error status. Distinct from the errors fetch
// itself throws (DNS/refused/timeout), which mean the server was never reached —
// a difference callers must be able to act on, since "wrong password" and "your
// server is unreachable" need very different advice. The message keeps the
// `${status} ${statusText}` prefix that call sites already match on.
export class ApiError extends Error {
	constructor(
		readonly status: number,
		message: string,
		// The daemon's machine-readable error code (e.g. SESSION_AWAITING_DECISION).
		// Carried separately from `message` so callers can branch on the exact
		// condition instead of pattern-matching human-facing prose.
		readonly code?: string,
		// Correlates a client-visible failure with daemon logs. The daemon's error
		// envelope guarantees this field, so mobile must not discard it.
		readonly requestId?: string,
		// The daemon's human-readable message alone, without the status prefix.
		// Screens render this (via userFacingError), never `message`.
		readonly detail?: string,
	) {
		super(message);
		this.name = "ApiError";
	}
}

export async function req(cfg: ServerConfig, path: string, init?: RequestInit, timeoutMs = REQUEST_TIMEOUT_MS): Promise<Response> {
	// Never plain http. A pairing saved before the desktop required TLS gets the
	// same 426 the desktop would answer, which the app shows as "re-pair".
	if (!cfg.secure) {
		const err = new InsecureTransportError(cfg.host, cfg.httpPort);
		throw new ApiError(err.status, `426 Upgrade Required - ${err.message}`, err.code, undefined, err.message);
	}
	const url = `${httpBase(cfg)}${path}`;
	// A machine that rejected this credential will reject it again. Answer
	// locally so no poll, retry or background job keeps sending it.
	if (isPairingRevoked(cfg.password)) {
		throw new ApiError(401, `401 Unauthorized - ${PAIRING_REMOVED_MESSAGE}`, "DEVICE_TOKEN_REVOKED", undefined, PAIRING_REMOVED_MESSAGE);
	}
	// Without a timeout a sleeping/unreachable host (common over Tailscale) hangs
	// the call for the OS TCP timeout (~75-120s), freezing Kill/send and the poll.
	const controller = new AbortController();
	const abortError = () => Object.assign(new Error("Request cancelled"), { name: "AbortError" });
	if (init?.signal?.aborted) throw abortError();
	const cancel = () => controller.abort();
	init?.signal?.addEventListener("abort", cancel);
	let rejectAbort!: () => void;
	const aborted = new Promise<never>((_resolve, reject) => {
		rejectAbort = () => reject(abortError());
		controller.signal.addEventListener("abort", rejectAbort);
	});
	const timer = setTimeout(cancel, timeoutMs);
	let received: Response | undefined;
	let bodyText = "";
	try {
		const installId = cachedInstallId();
		await Promise.race([aborted, (async () => {
			received = await fetch(url, {
				...init,
				signal: controller.signal,
				headers: {
					...authHeaders(cfg),
					"Content-Type": "application/json",
					...(installId ? { "X-AO-Install-Id": installId } : {}),
					...(init?.headers ?? {}),
				},
			});
			// This boundary serves finite JSON responses, not SSE or downloads.
			// Finish loading here so writes with no body consumer also release their
			// timer, and /sessions cannot fan out while its body is still stalled.
			bodyText = await received.text();
		})()]);
	} catch (e) {
		// Leaving a screen/host is not a connectivity failure. The poll owner
		// invalidates its result before cancelling the shared request lifecycle.
		if (init?.signal?.aborted) throw abortError();
		if (received && !received.ok) {
			// Headers already prove an auth/contract failure even if its optional
			// error envelope stalls. Never turn a 401 into a retryable timeout.
		} else if ((e as { name?: string })?.name === "AbortError") {
			// Timed out reaching the host (commonly a sleeping Tailscale peer).
			throw new UnreachableError("timeout", { cause: e });
		} else {
			throw new UnreachableError("offline", { cause: e });
		}
	} finally {
		clearTimeout(timer);
		init?.signal?.removeEventListener("abort", cancel);
		controller.signal.removeEventListener("abort", rejectAbort);
	}
	const res = received!;
	if (!res.ok) {
		// The daemon returns a locked JSON envelope: { error, code, message, requestId }.
		let detail = "";
		let code: string | undefined;
		let requestId: string | undefined;
		try {
			const body = JSON.parse(bodyText);
			detail = body?.message ?? body?.error ?? "";
			code = typeof body?.code === "string" ? body.code : undefined;
			requestId = typeof body?.requestId === "string" ? body.requestId : undefined;
		} catch {
			/* ignore */
		}
		// Every request carries X-AO-Expected-Host-ID, so a 401 comes from the
		// paired machine itself (another one answers 421): the pairing is gone.
		// Only for the codes that mean the pairing was dropped: a 401 can also come
		// from a route with an unrelated credential, and revoking on that would
		// disable a healthy token for good.
		if (res.status === 401 && revokesPairing(code)) markPairingRevoked(cfg.password);
		throw new ApiError(
			res.status,
			`${res.status} ${res.statusText}${detail ? ` - ${detail}` : ""}`,
			code,
			requestId,
			detail || undefined,
		);
	}
	// Keep the existing Response interface for callers; the network body is
	// already complete. Empty-response statuses cannot carry a Response body.
	return new Response([204, 205, 304].includes(res.status) ? null : bodyText, {
		status: res.status, statusText: res.statusText, headers: res.headers,
	});
}

/** Shared authenticated JSON request boundary for focused mobile feature modules. */
export function apiRequest(cfg: ServerConfig, path: string, init?: RequestInit, timeoutMs?: number): Promise<Response> {
	return req(cfg, path, init, timeoutMs);
}

