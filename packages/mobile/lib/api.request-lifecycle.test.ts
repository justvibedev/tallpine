import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ApiError, apiRequest, getSessions } from "./api";
import { UnreachableError } from "./connectionError";
import { isPairingRevoked, markPairingRevoked, resetRevokedPairings } from "./revokedPairing";


const config = { host: "tallpine.test", secure: true, httpPort: "3011", muxPort: "3011", password: "secret" };
beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("fetch", vi.fn()); });
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); resetRevokedPairings(); });

function stalledResponse(status = 200) {
	return new Response(new ReadableStream({ start() {} }), { status });
}

it("bounds a successful response whose headers arrive but body stalls", async () => {
	vi.mocked(fetch).mockResolvedValue(stalledResponse());
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(100);
	expect(failure).toBeInstanceOf(UnreachableError);
	expect(failure).toMatchObject({ reason: "timeout" });
});

it("retains a known auth status even when its error body stalls", async () => {
	vi.mocked(fetch).mockResolvedValue(stalledResponse(401));
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", undefined, 100).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(100);
	expect(failure).toBeInstanceOf(ApiError);
	expect(failure).toMatchObject({ status: 401 });
});

it("cancels a pending body read when the caller leaves the host", async () => {
	vi.mocked(fetch).mockResolvedValue(stalledResponse());
	const controller = new AbortController();
	let failure: unknown;
	void apiRequest(config, "/api/v1/sessions", { signal: controller.signal }).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	controller.abort();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toMatchObject({ name: "AbortError" });
});

it("does not send a request when the caller has already cancelled", async () => {
	const controller = new AbortController();
	controller.abort();
	vi.mocked(fetch).mockResolvedValue(new Response("{}"));
	await expect(apiRequest(config, "/api/v1/sessions", { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
	expect(fetch).not.toHaveBeenCalled();
});

it("keeps the daemon error envelope and clears completed request timers", async () => {
	vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ message: "Denied", code: "unauthorized", requestId: "req-1" }), { status: 401 }));
	await expect(apiRequest(config, "/api/v1/sessions")).rejects.toMatchObject({ status: 401, detail: "Denied", code: "unauthorized", requestId: "req-1" });
	expect(vi.getTimerCount()).toBe(0);
});

it("does not fan out board requests after the sessions body is cancelled", async () => {
	vi.mocked(fetch).mockImplementation(async (_url, init) => {
		if (init?.signal?.aborted) throw Object.assign(new Error("aborted"), { name: "AbortError" });
		return stalledResponse();
	});
	const controller = new AbortController();
	let failure: unknown;
	// Third argument scopes every request in this board load to one lifecycle.
	void getSessions(config, "all", { signal: controller.signal }).catch((error) => { failure = error; });
	await vi.advanceTimersByTimeAsync(1);
	controller.abort();
	await vi.advanceTimersByTimeAsync(1);
	expect(failure).toMatchObject({ name: "AbortError" });
	expect(fetch).toHaveBeenCalledTimes(1);
});

// Field bug: after the desktop revoked this phone, it kept sending the dead
// token every few seconds. One 401 must be the last request with it.
it("stops sending a credential once the computer answers 401", async () => {
	vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ code: "DEVICE_TOKEN_REVOKED", message: "removed" }), { status: 401 }));
	await expect(getSessions(config, "all")).rejects.toMatchObject({ status: 401 });
	expect(isPairingRevoked("secret")).toBe(true);
	const sent = vi.mocked(fetch).mock.calls.length;
	for (let i = 0; i < 5; i++) {
		await expect(apiRequest(config, "/api/v1/endpoints")).rejects.toMatchObject({ status: 401, code: "DEVICE_TOKEN_REVOKED" });
	}
	expect(fetch).toHaveBeenCalledTimes(sent);
	// Another machine's credential is unaffected.
	vi.mocked(fetch).mockResolvedValue(new Response("{}"));
	await apiRequest({ ...config, password: "other" }, "/api/v1/sessions");
	expect(fetch).toHaveBeenCalledTimes(sent + 1);
});

it("does not mark a credential on a lockout or a misdirected request", async () => {
	for (const status of [429, 421, 403]) {
		vi.mocked(fetch).mockResolvedValueOnce(new Response("{}", { status }));
		await expect(apiRequest(config, "/api/v1/sessions")).rejects.toMatchObject({ status });
	}
	expect(isPairingRevoked("secret")).toBe(false);
	markPairingRevoked("other");
	expect(isPairingRevoked("secret")).toBe(false);
});

// A 401 from a route with an unrelated credential is not the pairing being
// dropped; revoking on it would disable a healthy device token for good.
it("does not revoke the pairing on a 401 that is not a credential code", async () => {
	vi.mocked(fetch).mockResolvedValue(new Response(JSON.stringify({ code: "GITHUB_AUTH_INVALID", message: "bad github token" }), { status: 401 }));
	await expect(apiRequest(config, "/api/v1/github/x")).rejects.toMatchObject({ status: 401, code: "GITHUB_AUTH_INVALID" });
	expect(isPairingRevoked("secret")).toBe(false);
	await expect(apiRequest(config, "/api/v1/github/x")).rejects.toMatchObject({ status: 401 });
	expect(fetch).toHaveBeenCalledTimes(2);
});
