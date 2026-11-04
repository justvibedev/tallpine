import { describe, expect, it } from "vitest";
import { fromBase64Url, toBase64Url } from "./base64url";

describe("base64url", () => {
	it.each(["", "a", "ab", "abc", "Alex’s MacBook", "café Ünï", "emoji 👨‍👩‍👧 🚀", '{"a":"???>>>"}'])("round-trips %j", (s) => {
		expect(fromBase64Url(toBase64Url(s))).toBe(s);
	});

	it("is unpadded and URL-safe", () => {
		const out = toBase64Url("???>>>~~~ÿÿ");
		expect(out).not.toMatch(/[+/=]/);
	});

	it("leaves ASCII output identical to plain base64", () => {
		expect(toBase64Url("hello")).toBe("aGVsbG8");
	});

	it("rejects non-base64 input", () => {
		expect(() => fromBase64Url("!!!")).toThrow();
	});
});
