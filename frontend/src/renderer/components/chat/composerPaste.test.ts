import { describe, expect, it } from "vitest";
import { isRawPasteShortcut, pasteLineCount, pastePreview, shouldCollapsePaste } from "./composerPaste";

describe("shouldCollapsePaste", () => {
	it("keeps short single- and two-line text inline", () => {
		expect(shouldCollapsePaste("hello")).toBe(false);
		expect(shouldCollapsePaste("a\nb")).toBe(false);
		expect(shouldCollapsePaste("a\nb\n")).toBe(false);
	});
	it("collapses at 3 lines", () => {
		expect(shouldCollapsePaste("a\nb\nc")).toBe(true);
	});
	it("collapses at 200 chars but not 199", () => {
		expect(shouldCollapsePaste("x".repeat(199))).toBe(false);
		expect(shouldCollapsePaste("x".repeat(200))).toBe(true);
	});
});

describe("paste helpers", () => {
	it("counts lines, ignoring one trailing newline and CRLF", () => {
		expect(pasteLineCount("a\r\nb\r\nc\r\n")).toBe(3);
		expect(pasteLineCount("")).toBe(0);
	});
	it("previews the first 12 lines and reports the rest", () => {
		const text = Array.from({ length: 15 }, (_, i) => `l${i + 1}`).join("\n");
		const preview = pastePreview(text);
		expect(preview.text.split("\n")).toHaveLength(12);
		expect(preview.hiddenLines).toBe(3);
	});
	it("recognises Cmd/Ctrl+Shift+V only", () => {
		const base = { key: "V", metaKey: true, ctrlKey: false, shiftKey: true };
		expect(isRawPasteShortcut(base)).toBe(true);
		expect(isRawPasteShortcut({ ...base, shiftKey: false })).toBe(false);
		expect(isRawPasteShortcut({ ...base, metaKey: false })).toBe(false);
	});
});
