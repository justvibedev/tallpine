import { beforeEach, describe, expect, it, vi } from "vitest";

(globalThis as { expo?: unknown }).expo = { modules: { TallpinePinning: { setPins: vi.fn(), probe: vi.fn() } } };

import { installPinnedFetch } from "./pinnedFetch";
import { registerPins, resetPinRegistrationForTest } from "./pinning";

const PIN = "ab".repeat(32);

function setup(os: string) {
	const original = vi.fn<typeof fetch>(async () => new Response("expo"));
	const rn = vi.fn<typeof fetch>(async () => new Response("rn"));
	const target = { fetch: original as typeof fetch };
	installPinnedFetch({ os, rnFetch: () => rn, target });
	return { original, rn, target };
}

beforeEach(() => {
	resetPinRegistrationForTest();
	registerPins([{ kind: "lan", host: "192.168.1.5", port: 3011, secure: true, pin: PIN }]);
});

describe("installPinnedFetch", () => {
	it("sends requests to a pinned host:port through RN's fetch on iOS", async () => {
		const { original, rn, target } = setup("ios");
		await target.fetch("https://192.168.1.5:3011/api/v1/identity");
		expect(rn).toHaveBeenCalledOnce();
		expect(original).not.toHaveBeenCalled();
	});
	it("leaves other hosts and ports on the original fetch", async () => {
		const { original, rn, target } = setup("ios");
		await target.fetch("https://example.com/x");
		await target.fetch("https://192.168.1.5:9999/x");
		await target.fetch("http://192.168.1.5:3011/x");
		expect(original).toHaveBeenCalledTimes(3);
		expect(rn).not.toHaveBeenCalled();
	});
	it("does nothing on Android, where expo/fetch already uses the pinned client", async () => {
		const { original, target } = setup("android");
		await target.fetch("https://192.168.1.5:3011/x");
		expect(original).toHaveBeenCalledOnce();
	});
});
