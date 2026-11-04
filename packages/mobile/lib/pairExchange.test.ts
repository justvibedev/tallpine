import { describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));

import { connectHost } from "./connect";
import type { Endpoint } from "./endpoints";
import type { Host } from "./hosts";
import { exchangeForDeviceToken } from "./pairExchange";
import { pairFromCode } from "./pairFlow";
import { encodePairingCode } from "./pairingCode";

const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: true };
const DEVICE = "vot_" + "a".repeat(43);

const okFetch = (token: string = DEVICE) =>
	vi.fn(async () => ({ ok: true, status: 200, json: async () => ({ token }) }));

const exDeps = (fetchImpl = okFetch()) => ({ fetch: fetchImpl, installId: async () => "install-1", deviceName: "Pixel" });

describe("exchangeForDeviceToken", () => {
	it("posts the code and install id and returns the device token", async () => {
		const f = okFetch();
		const got = await exchangeForDeviceToken(lan, { code: "Code1234" }, exDeps(f));
		expect(got).toEqual({ ok: true, token: DEVICE });
		const [url, init] = f.mock.calls[0] as unknown as [string, { body: string; headers: Record<string, string> }];
		expect(url).toBe("https://192.168.1.42:3011/pair/exchange");
		expect(JSON.parse(init.body)).toMatchObject({ code: "Code1234", installId: "install-1" });
		expect(init.headers.Authorization).toBeUndefined();
	});

	it("sends a legacy password as the bearer, not in the body", async () => {
		const f = okFetch();
		await exchangeForDeviceToken(lan, { legacyBearer: "oldpass1" }, exDeps(f));
		const init = (f.mock.calls[0] as unknown as [string, { body: string; headers: Record<string, string> }])[1];
		expect(init.headers.Authorization).toBe("Bearer oldpass1");
		expect(init.body).not.toContain("oldpass1");
	});

	it("rejects a response whose token is not a device token", async () => {
		expect(await exchangeForDeviceToken(lan, { code: "x" }, exDeps(okFetch("plain")))).toEqual({ ok: false, status: 200 });
	});

	it("reports a network failure without throwing", async () => {
		const f = vi.fn(async () => { throw new Error("down"); });
		expect(await exchangeForDeviceToken(lan, { code: "x" }, exDeps(f as never))).toEqual({ ok: false });
	});
});

const code = encodePairingCode({ v: 2, hostId: "h1", name: "mbp", platform: "darwin", endpoints: [lan], token: "Code1234" });
const pairDeps = (over: Record<string, unknown> = {}) => ({
	race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h1" })),
	verify: vi.fn(async () => {}),
	chooseName: vi.fn(async (s: string, a: never) => ({ name: s, appearance: a })),
	saveHost: vi.fn(async () => {}),
	setActiveHost: vi.fn(async () => {}),
	...over,
});

describe("pairFromCode with the exchange", () => {
	it("stores the device token, never the pairing code", async () => {
		const d = pairDeps({ exchange: vi.fn(async () => ({ ok: true as const, token: DEVICE })) });
		const got = await pairFromCode(code, d as never);
		expect(got.ok).toBe(true);
		expect(d.saveHost).toHaveBeenCalledWith(expect.objectContaining({ token: DEVICE }));
		expect(d.verify).toHaveBeenCalledWith(expect.objectContaining({ password: DEVICE }));
	});

	it("falls back to the code as the bearer against a daemon without the route", async () => {
		const d = pairDeps({ exchange: vi.fn(async () => ({ ok: false as const, status: 404 })) });
		await pairFromCode(code, d as never);
		expect(d.saveHost).toHaveBeenCalledWith(expect.objectContaining({ token: "Code1234" }));
	});

	it("stops with the daemon's status when the code was refused", async () => {
		const d = pairDeps({ exchange: vi.fn(async () => ({ ok: false as const, status: 401 })) });
		const got = await pairFromCode(code, d as never);
		expect(got).toMatchObject({ ok: false, reason: "verify-failed", status: 401 });
		expect(d.saveHost).not.toHaveBeenCalled();
	});
});

describe("connectHost legacy upgrade", () => {
	const host = (token: string): Host => ({ id: "h1", name: "mbp", platform: "darwin", endpoints: [lan], token, lastConnected: 1 });
	const base = (token: string, over: Record<string, unknown> = {}) => ({
		findHost: vi.fn(async () => host(token)),
		race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h1" })),
		refreshEndpoints: vi.fn(async () => [lan]),
		saveEndpoints: vi.fn(async () => {}),
		adoptIdentity: vi.fn(async () => {}),
		touch: vi.fn(async () => {}),
		upgradeToken: vi.fn(async () => DEVICE),
		saveToken: vi.fn(async () => {}),
		...over,
	});

	it("replaces a stored legacy password with a device token on connect", async () => {
		const d = base("oldpass1");
		const got = await connectHost("h1", d);
		expect(d.saveToken).toHaveBeenCalledWith("h1", DEVICE, "oldpass1");
		expect(got.ok && got.config.password).toBe(DEVICE);
	});

	it("does not exchange a token that is already a device token", async () => {
		const d = base(DEVICE);
		await connectHost("h1", d);
		expect(d.upgradeToken).not.toHaveBeenCalled();
	});

	it("keeps the old password when the upgrade fails", async () => {
		const d = base("oldpass1", { upgradeToken: vi.fn(async () => null) });
		const got = await connectHost("h1", d);
		expect(got.ok && got.config.password).toBe("oldpass1");
		expect(d.saveToken).not.toHaveBeenCalled();
	});
});
