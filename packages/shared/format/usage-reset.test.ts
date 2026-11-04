import { describe, expect, it } from "vitest";
import { formatUsageReset } from "./usage-reset";

describe("formatUsageReset", () => {
	it("formats a same-day reset as a clock time and a later one with its date", () => {
		const now = new Date("2026-11-02T03:22:00Z");
		expect(formatUsageReset("2026-11-02T06:20:00Z", "en-US", now)).toMatch(/\d:\d\d/);
		expect(formatUsageReset("2026-11-04T06:20:00Z", "en-US", now)).toMatch(/Nov/);
	});
});
