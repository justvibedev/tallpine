import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, apiRequest } from "./api";
import { browserLiveURL } from "./browserLiveProtocol";
import { httpBase, muxUrl } from "./config";
import { classifyConnectionFailure, describeConnectionFailure } from "./connectionError";
import { endpointBaseUrl, endpointDisplayUrl } from "./endpoints";
import { encodePairingCode, parsePairingCode } from "./pairingCode";
import { secureEndpoints } from "./pinning";
import { InsecureTransportError, secureOrigin } from "./secureTransport";

// A pairing saved by an older build that still names a plain-http address.
const plain = { host: "192.168.1.5", httpPort: "3011", muxPort: "3011", password: "vot_x", secure: false };
const pin = "a".repeat(64);

beforeEach(() => { vi.stubGlobal("fetch", vi.fn()); });
afterEach(() => { vi.unstubAllGlobals(); });

describe("the phone never builds a plain http URL", () => {
	it("refuses every URL builder for a plain address", () => {
		expect(() => secureOrigin({ host: "h", port: 1 })).toThrow(InsecureTransportError);
		expect(() => httpBase(plain)).toThrow(InsecureTransportError);
		expect(() => muxUrl(plain)).toThrow(InsecureTransportError);
		expect(() => browserLiveURL(plain, "s-1")).toThrow(InsecureTransportError);
		expect(() => endpointBaseUrl({ kind: "lan", host: "192.168.1.5", port: 3011, secure: false })).toThrow(InsecureTransportError);
	});

	it("builds https and wss for a secure address", () => {
		const cfg = { ...plain, secure: true };
		expect(httpBase(cfg)).toBe("https://192.168.1.5:3011");
		expect(muxUrl(cfg)).toBe("wss://192.168.1.5:3011/mux");
		expect(browserLiveURL(cfg, "s-1")).toBe("wss://192.168.1.5:3011/api/v1/sessions/s-1/browser/live");
	});

	it("still shows a plain stored endpoint's address without connecting to it", () => {
		expect(endpointDisplayUrl({ kind: "lan", host: "192.168.1.5", port: 3011, secure: false })).toBe("http://192.168.1.5:3011");
	});
});

describe("a pairing saved on a plain address", () => {
	it("gets the re-pair error without sending anything", async () => {
		const err = await apiRequest(plain, "/api/v1/sessions").catch((e: unknown) => e);
		expect(err).toBeInstanceOf(ApiError);
		expect(err).toMatchObject({ status: 426, code: "HTTPS_REQUIRED" });
		expect(fetch).not.toHaveBeenCalled();
		const failure = classifyConnectionFailure((err as ApiError).status, (err as ApiError).code);
		expect(failure).toBe("https-required");
		expect(describeConnectionFailure(failure, { host: plain.host, port: plain.httpPort, platform: "ios" }).title).toBe("Re-pair for a secure connection");
	});
});

describe("pairing only accepts secure endpoints", () => {
	const offer = { v: 2 as const, hostId: "h_1", name: "Mac", platform: "darwin", token: "t" };

	it("drops plain endpoints from a pairing code and keeps pinned and trusted ones", () => {
		const parsed = parsePairingCode(encodePairingCode({
			...offer,
			endpoints: [
				{ kind: "lan", host: "192.168.1.5", port: 3011, secure: false },
				{ kind: "lan", host: "192.168.1.6", port: 3011, secure: false, pin },
				{ kind: "relay", host: "relay.example.com", port: 80, secure: false },
				{ kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true },
			],
		}));
		expect(parsed?.endpoints.map((e) => e.host)).toEqual(["192.168.1.6", "abc.trycloudflare.com"]);
		expect(parsed?.endpoints.every((e) => e.secure)).toBe(true);
	});

	it("rejects a code whose only endpoints are plain http", () => {
		expect(parsePairingCode(encodePairingCode({ ...offer, endpoints: [{ kind: "lan", host: "192.168.1.5", port: 3011, secure: false }] }))).toBeNull();
	});

	it("filters refreshed endpoint lists the same way", () => {
		expect(secureEndpoints([
			{ kind: "tailscale", host: "100.1.2.3", port: 3011, secure: false },
			{ kind: "tailscale", host: "100.1.2.4", port: 3011, secure: false, pin },
		]).map((e) => e.host)).toEqual(["100.1.2.4"]);
	});
});
