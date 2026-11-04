import { beforeEach, describe, expect, it, vi } from "vitest";

const setPins = vi.fn();
(globalThis as { expo?: unknown }).expo = { modules: { TallpinePinning: { setPins, probe: vi.fn() } } };

import { endpointBaseUrl } from "./endpoints";
import { isValidPin, normalizeEndpoint, pinEntries, registerPins, resetPinRegistrationForTest, shortFingerprint, stageOfferPins } from "./pinning";

const PIN = "abababababababababababababababababababababababababababababababab";
const lan = { kind: "lan" as const, host: "192.168.1.42", port: 65221, secure: false };

beforeEach(() => {
	setPins.mockClear();
	resetPinRegistrationForTest();
});

describe("normalizeEndpoint", () => {
	it("makes a pinned endpoint https", () => {
		const e = normalizeEndpoint({ ...lan, pin: PIN });
		expect(e.secure).toBe(true);
		expect(endpointBaseUrl(e)).toBe("https://192.168.1.42:65221");
	});
	it("drops a malformed pin instead of trusting it", () => {
		const e = normalizeEndpoint({ ...lan, pin: "not-a-pin" });
		expect(e.pin).toBeUndefined();
		expect(e.secure).toBe(false);
	});
	it("leaves an unpinned endpoint alone", () => {
		expect(normalizeEndpoint(lan)).toEqual(lan);
	});
});

describe("registerPins", () => {
	it("hands host:port=pin to the native layer, once", () => {
		const e = normalizeEndpoint({ ...lan, pin: PIN });
		registerPins([e]);
		registerPins([e]);
		expect(setPins).toHaveBeenCalledTimes(1);
		expect(setPins).toHaveBeenCalledWith(["192.168.1.42:65221=" + PIN]);
	});
	it("ignores endpoints without a pin", () => {
		registerPins([lan]);
		expect(setPins).not.toHaveBeenCalled();
	});
	it("keeps a second computer's pin without dropping the first", () => {
		registerPins([{ ...lan, pin: PIN }]);
		registerPins([{ ...lan, host: "192.168.1.50", pin: PIN.replace(/a/g, "c") }]);
		expect(setPins).toHaveBeenCalledTimes(2);
	});
});

describe("helpers", () => {
	it("validates pins as 64 lowercase hex", () => {
		expect(isValidPin(PIN)).toBe(true);
		expect(isValidPin(PIN.toUpperCase())).toBe(false);
		expect(pinEntries([{ ...lan, pin: "short" }])).toEqual([]);
	});
	it("groups a fingerprint for reading aloud", () => {
		expect(shortFingerprint(PIN)).toBe("abab abab abab abab");
	});
});

describe("missing native module", () => {
	it("warns instead of silently leaving pinned hosts unpinned", () => {
		const saved = (globalThis as { expo?: unknown }).expo;
		(globalThis as { expo?: unknown }).expo = undefined;
		const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
		registerPins([{ ...lan, pin: PIN }]);
		expect(warn).toHaveBeenCalled();
		warn.mockRestore();
		(globalThis as { expo?: unknown }).expo = saved;
	});
});

describe("pin changes and untrusted offers", () => {
	const PIN_B = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd";
	it("re-sends a host:port whose pin changed", () => {
		registerPins([normalizeEndpoint({ ...lan, pin: PIN })]);
		registerPins([normalizeEndpoint({ ...lan, pin: PIN_B })]);
		expect(setPins).toHaveBeenLastCalledWith(["192.168.1.42:65221=" + PIN_B]);
	});
	it("puts the saved pin back when a staged offer is abandoned", () => {
		registerPins([normalizeEndpoint({ ...lan, pin: PIN })]);
		const restore = stageOfferPins([normalizeEndpoint({ ...lan, pin: PIN_B })]);
		expect(setPins).toHaveBeenLastCalledWith(["192.168.1.42:65221=" + PIN_B]);
		restore();
		expect(setPins).toHaveBeenLastCalledWith(["192.168.1.42:65221=" + PIN]);
		registerPins([normalizeEndpoint({ ...lan, pin: PIN })]);
		expect(setPins).toHaveBeenCalledTimes(3);
	});
});
