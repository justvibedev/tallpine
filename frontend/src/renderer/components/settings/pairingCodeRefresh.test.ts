import { expect, it } from "vitest";
import { PAIRING_CODE_REFRESH_MS, withPairingCodeRefresh } from "./pairingCodeRefresh";

it("keeps polling while mobile is on so a redeemed or expired code is replaced", () => {
	expect(withPairingCodeRefresh(false, true)).toBe(PAIRING_CODE_REFRESH_MS);
});

it("does not poll while mobile is off", () => {
	expect(withPairingCodeRefresh(false, false)).toBe(false);
	expect(withPairingCodeRefresh(false, undefined)).toBe(false);
});

it("keeps the faster readiness poll when pairing is still pending", () => {
	expect(withPairingCodeRefresh(2_000, true)).toBe(2_000);
});
