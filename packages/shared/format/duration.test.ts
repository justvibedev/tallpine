import { describe, expect, it } from "vitest";
import { formatDurationClock, formatDurationLocale, formatDurationStatus, formatRelativeLong, formatResetIn } from "./duration";

describe("duration variants keep their previous output", () => {
	it("status", () => {
		expect([0, 38_400, 59_999, 60_000, 3_600_000, 3_720_000].map(formatDurationStatus)).toEqual(["1s", "38s", "59s", "1m", "1h", "1h 2m"]);
	});
	it("clock", () => {
		expect([-5, 38_400, 250_000, 3_720_000].map(formatDurationClock)).toEqual(["0s", "38s", "4m 10s", "1h 2m"]);
	});
	it("locale", () => {
		expect([-1, 9, 240, 3900].map((s) => formatDurationLocale(s, "en"))).toEqual(["0s", "9s", "4m", "1h 5m"]);
	});
	it("reset in", () => {
		expect([0, 30, 300, 3 * 3600, 4 * 86400].map(formatResetIn)).toEqual(["now", "1m", "5m", "3h", "4d"]);
	});
});

describe("formatRelativeLong", () => {
	const now = Date.parse("2026-01-10T12:00:00Z");
	it("picks the unit and returns justNow when recent or invalid", () => {
		expect(formatRelativeLong("2026-01-10T11:59:30Z", "just now", now, "en")).toBe("just now");
		expect(formatRelativeLong("garbage", "just now", now, "en")).toBe("just now");
		expect(formatRelativeLong("2026-01-10T11:55:00Z", "x", now, "en")).toBe("5 minutes ago");
		expect(formatRelativeLong("2026-01-10T09:00:00Z", "x", now, "en")).toBe("3 hours ago");
		expect(formatRelativeLong("2026-01-07T12:00:00Z", "x", now, "en")).toBe("3 days ago");
	});
});
