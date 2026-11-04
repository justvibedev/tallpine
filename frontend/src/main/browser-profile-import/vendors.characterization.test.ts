import { describe, expect, it } from "vitest";
import { chromiumSameSite, chromiumTimestamp } from "./chromium";
import { dedupeCookies, mergeWarnings } from "./cookie-rows";
import { firefoxSameSite } from "./firefox";
import { safariSameSite, safariTimestamp } from "./safari";

// Characterization of vendor helpers moved out of browser-profile-import.ts (B297, P53).
describe("vendor timestamp and SameSite mapping", () => {
	it("converts Chromium (µs since 1601) and Safari (s since 2001) to Unix seconds; invalid -> 0", () => {
		expect(chromiumTimestamp(11_644_473_600 * 1_000_000 + 5_000_000)).toBe(5);
		expect(chromiumTimestamp(0)).toBe(0);
		expect(chromiumTimestamp(Number.NaN)).toBe(0);
		expect(safariTimestamp(10)).toBe(978_307_210);
		expect(safariTimestamp(-1)).toBe(0);
	});

	it("maps each vendor's SameSite codes", () => {
		for (const fn of [chromiumSameSite, firefoxSameSite]) {
			expect([0, 1, 2, 9].map(fn)).toEqual(["no_restriction", "lax", "strict", "unspecified"]);
		}
		expect([4, 5, 7, 0].map(safariSameSite)).toEqual(["no_restriction", "lax", "strict", "unspecified"]);
	});
});

describe("cookie row helpers", () => {
	const cookie = (dedupeKey: string, expirationDate?: number) => ({ dedupeKey, expirationDate, url: "https://a.test", name: dedupeKey }) as never;

	it("dedupeCookies keeps the first selected profile's cookie and sorts by expiry then key", () => {
		const first = cookie("k1", 10);
		const result = dedupeCookies([first, cookie("k1", 99), cookie("k2", 50)]);
		expect(result.cookies.map((c: { dedupeKey: string }) => c.dedupeKey)).toEqual(["k2", "k1"]);
		expect(result.cookies[1]).toBe(first);
		expect(result.truncated).toBe(0);
	});

	it("mergeWarnings sums counts per code and keeps count-less warnings", () => {
		const merged = mergeWarnings([
			{ code: "cookie_skipped", count: 2 },
			{ code: "cookie_skipped", count: 3 },
			{ code: "history_truncated" },
		] as never);
		expect(merged).toEqual([{ code: "cookie_skipped", count: 5 }, { code: "history_truncated" }]);
	});
});
