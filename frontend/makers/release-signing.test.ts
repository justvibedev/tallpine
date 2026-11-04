import { describe, expect, it } from "vitest";
import { assertMacSigning, hasHardenedRuntime, isAdHoc, type CodesignRunner } from "./release-signing";

const HARDENED = "CodeDirectory v=20500 size=1 flags=0x10000(runtime) hashes=1 location=embedded\nSignature size=9000";
const NO_RUNTIME = "CodeDirectory v=20400 size=1 flags=0x0(none) hashes=1 location=embedded\nSignature size=9000";
const ADHOC = "CodeDirectory v=20400 size=1 flags=0x2(adhoc) hashes=1 location=embedded\nSignature=adhoc";

function runner(verifyStatus: number, details: string): CodesignRunner {
	return (args) => (args[0] === "--verify" ? { status: verifyStatus, output: "" } : { status: 0, output: details });
}

describe("codesign output parsing", () => {
	it("detects the hardened runtime flag", () => {
		expect(hasHardenedRuntime(HARDENED)).toBe(true);
		expect(hasHardenedRuntime(NO_RUNTIME)).toBe(false);
		expect(hasHardenedRuntime(ADHOC)).toBe(false);
	});
	it("detects ad hoc signatures", () => {
		expect(isAdHoc(ADHOC)).toBe(true);
		expect(isAdHoc(HARDENED)).toBe(false);
	});
});

describe("assertMacSigning", () => {
	it("refuses an unsigned build by default", () => {
		expect(() => assertMacSigning("/x.app", false, {}, runner(0, HARDENED))).toThrow(/Refusing to produce a release artifact/);
	});
	it("allows unsigned only with the explicit flag, and warns loudly", () => {
		const warnings = assertMacSigning("/x.app", false, { TALLPINE_ALLOW_UNSIGNED: "1" }, runner(1, ""));
		expect(warnings[0]).toMatch(/UNSIGNED BUILD/);
	});
	it("rejects a signed app that fails codesign --verify", () => {
		expect(() => assertMacSigning("/x.app", true, {}, runner(1, HARDENED))).toThrow(/--verify --deep --strict failed/);
	});
	it("rejects a signed app without hardened runtime", () => {
		expect(() => assertMacSigning("/x.app", true, {}, runner(0, NO_RUNTIME))).toThrow(/hardened runtime/);
	});
	it("rejects an ad hoc signature when signing was configured", () => {
		expect(() => assertMacSigning("/x.app", true, {}, runner(0, ADHOC))).toThrow(/ad hoc/);
	});
	it("accepts a verified hardened-runtime signature", () => {
		expect(assertMacSigning("/x.app", true, {}, runner(0, HARDENED))).toEqual([]);
	});
	it("does not let the allow flag skip checks on a signed app", () => {
		expect(() => assertMacSigning("/x.app", true, { TALLPINE_ALLOW_UNSIGNED: "1" }, runner(0, NO_RUNTIME))).toThrow();
	});
});
