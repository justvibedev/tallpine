import { beforeEach, describe, expect, it, vi } from "vitest";

const store = vi.hoisted(() => ({ get: vi.fn(), set: vi.fn() }));
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: store.get,
	setItemAsync: store.set,
	deleteItemAsync: vi.fn(),
}));

import { connectHost, type ConnectDeps } from "./connect";
import type { Endpoint } from "./endpoints";
import { type Host, saveHostToken } from "./hosts";

const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: false };
const legacyHost: Host = { id: "h_paired", name: "mbp", platform: "darwin", endpoints: [lan], token: "oldpass1", lastConnected: 1 };

function deps(over: Partial<ConnectDeps> = {}): ConnectDeps {
	return {
		findHost: vi.fn(async () => legacyHost),
		race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h_paired" })),
		refreshEndpoints: vi.fn(async () => []),
		saveEndpoints: vi.fn(async () => {}),
		adoptIdentity: vi.fn(async () => {}),
		touch: vi.fn(async () => {}),
		upgradeToken: vi.fn(async () => "vot_new"),
		saveToken: vi.fn(async () => true),
		...over,
	};
}

describe("upgrading a legacy password to a device token", () => {
	it("single-flights concurrent connects: one exchange, one saved token, both use it", async () => {
		let release!: (t: string) => void;
		const upgradeToken = vi.fn(() => new Promise<string>((r) => { release = r; }));
		const d = deps({ upgradeToken });

		const a = connectHost("h_paired", d);
		const b = connectHost("h_paired", d);
		await vi.waitFor(() => expect(upgradeToken).toHaveBeenCalled());
		release("vot_new");
		const [ra, rb] = await Promise.all([a, b]);

		expect(upgradeToken).toHaveBeenCalledTimes(1);
		expect(d.saveToken).toHaveBeenCalledTimes(1);
		expect(ra.ok && ra.config.password).toBe("vot_new");
		expect(rb.ok && rb.config.password).toBe("vot_new");
	});

	it("saves only while storage still holds the legacy value it started from", async () => {
		const d = deps();
		await connectHost("h_paired", d);
		expect(d.saveToken).toHaveBeenCalledWith("h_paired", "vot_new", "oldpass1");
	});

	it("stays on the legacy credential when the save is refused", async () => {
		const d = deps({ saveToken: vi.fn(async () => false) });
		const r = await connectHost("h_paired", d);
		expect(r.ok && r.config.password).toBe("oldpass1");
	});

	it("retries a failed save once, then keeps the legacy credential rather than reporting an upgrade", async () => {
		const failing = deps({ saveToken: vi.fn(async () => { throw new Error("keychain locked"); }) });
		const r = await connectHost("h_paired", failing);
		expect(failing.saveToken).toHaveBeenCalledTimes(2);
		expect(r.ok && r.config.password).toBe("oldpass1");

		const flaky = deps({ saveToken: vi.fn().mockRejectedValueOnce(new Error("busy")).mockResolvedValue(true) });
		const ok = await connectHost("h_paired", flaky);
		expect(ok.ok && ok.config.password).toBe("vot_new");
	});
});

describe("saveHostToken compare-and-set", () => {
	beforeEach(() => { store.get.mockReset(); store.set.mockReset(); });

	it("refuses to overwrite a token that changed since the upgrade began", async () => {
		store.get.mockResolvedValue("vot_repaired");
		expect(await saveHostToken("h_paired", "vot_stale", "oldpass1")).toBe(false);
		expect(store.set).not.toHaveBeenCalled();
	});

	it("writes when the stored token is still the expected one", async () => {
		store.get.mockResolvedValue("oldpass1");
		expect(await saveHostToken("h_paired", "vot_new", "oldpass1")).toBe(true);
		expect(store.set).toHaveBeenCalledTimes(1);
	});
});
