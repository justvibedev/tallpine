import { describe, expect, it } from "vitest";
import { effectiveAutoCompact, parseAutoCompactPercent, supportsAutoCompact } from "./auto-compact";

describe("effectiveAutoCompact", () => {
	it("prefers the session's choice over the default", () => {
		expect(effectiveAutoCompact("80%", "50%")).toBe("80%");
		expect(effectiveAutoCompact(undefined, "50%")).toBe("50%");
		expect(effectiveAutoCompact(undefined, undefined)).toBeUndefined();
	});
});

describe("supportsAutoCompact", () => {
	it("supports Claude Code, Codex terminals only, and nothing else", () => {
		expect(supportsAutoCompact("claude-code", "sonnet")).toBe("yes");
		expect(supportsAutoCompact("claude-code", "haiku")).toBe("no");
		expect(supportsAutoCompact("codex", "", true)).toBe("yes");
		expect(supportsAutoCompact("codex", "", false)).toBe("unsupported");
		expect(supportsAutoCompact("gemini")).toBe("no");
	});
});

describe("parseAutoCompactPercent", () => {
	it("refuses everything that is not a whole percent from 20 to 95", () => {
		for (const bad of ["", "200k", "1m", "19%", "96%", "100%", "0%", "4%", "040%", "-40%", "+40%", "4 0%", "40.5%", "%", "300k", "70", "40%%", " 40%"]) {
			expect(parseAutoCompactPercent(bad), bad).toBeUndefined();
		}
		for (const ok of ["20%", "40%", "95%"]) expect(parseAutoCompactPercent(ok), ok).toBeDefined();
	});
	it("parses 40% and an off-step percent, rejecting out-of-range ones", () => {
		expect(parseAutoCompactPercent("40%")).toBe(40);
		expect(parseAutoCompactPercent("75%")).toBe(75);
		expect(parseAutoCompactPercent("19%")).toBeUndefined();
		expect(parseAutoCompactPercent("96%")).toBeUndefined();
	});
});
