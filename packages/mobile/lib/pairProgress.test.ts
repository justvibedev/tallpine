import { describe, expect, it } from "vitest";
import { formatPairDetails, hasStarted, markStep, noteProgress, redactSecrets, startPairProgress } from "./pairProgress";

const T0 = new Date(2026, 9, 31, 11, 32, 47).getTime();

describe("pair progress", () => {
	it("tracks step states and logs plain-language lines", () => {
		let p = startPairProgress(T0);
		expect(hasStarted(p)).toBe(false);
		p = markStep(p, "find", "active", T0);
		p = markStep(p, "find", "done", T0 + 1000, "at 192.168.1.196:65221");
		p = markStep(p, "security", "active", T0 + 1000);
		expect(hasStarted(p)).toBe(true);
		expect(p.steps).toEqual({ find: "done", security: "active", paired: "pending", load: "pending" });
		expect(p.log.map((l) => l.text)).toEqual([
			"Code scanned",
			"Looking for your computer",
			"Found your computer (at 192.168.1.196:65221)",
			"Checking security code",
		]);
	});

	it("never copies a pairing link, token or certificate pin", () => {
		const token = "dGhpc2lzYXZlcnlsb25nZGV2aWNldG9rZW52YWx1ZQ"; // gitleaks:allow
		const pin = "722b8f0be41ac5247f1039d03d4e0ce536a6df73feb7532fbac3a753b2fe213a";
		let p = startPairProgress(T0);
		p = noteProgress(p, `opened tallpine-mobile://pair#eyJ2IjoyLCJ0b2tlbiI6InNlY3JldCJ9`, T0);
		p = noteProgress(p, `Authorization: Bearer ${token}`, T0);
		p = noteProgress(p, `pin ${pin}`, T0);
		p = noteProgress(p, `token=abc123`, T0);
		const text = formatPairDetails(p, { app: "Tallpine", platform: "ios" });
		for (const secret of [token, pin, "eyJ2IjoyLCJ0b2tlbiI6InNlY3JldCJ9", "abc123"]) expect(text).not.toContain(secret);
		expect(text).toContain("tallpine-mobile://pair#[hidden]");
	});

	it("keeps addresses, which make the log useful", () => {
		expect(redactSecrets("Found your computer (at 192.168.1.196:65221)")).toBe("Found your computer (at 192.168.1.196:65221)");
	});

	it("formats a short shareable summary", () => {
		let p = startPairProgress(T0);
		p = markStep(p, "find", "done", T0);
		p = markStep(p, "load", "failed", T0 + 5000);
		const text = formatPairDetails(p, { app: "Tallpine", platform: "ios" });
		expect(text.split("\n").slice(0, 5)).toEqual([
			"Tallpine pairing details (ios)",
			"- Find computer: done",
			"- Security code: pending",
			"- Pairing: pending",
			"- Load sessions: failed",
		]);
		expect(text).toContain("11:32:52 Sessions didn't load");
	});

	it("bounds the log", () => {
		let p = startPairProgress(T0);
		for (let i = 0; i < 100; i++) p = noteProgress(p, `retry ${i}`, T0);
		expect(p.log.length).toBe(40);
		expect(p.log.at(-1)?.text).toBe("retry 99");
	});
});

describe("redactSecrets keeps the step wording", () => {
	it("does not hide words after 'code' in labels", () => {
		expect(redactSecrets("Security code checked")).toBe("Security code checked");
		expect(redactSecrets("Security code not accepted (the code was already used)")).toBe("Security code not accepted (the code was already used)");
	});
});
