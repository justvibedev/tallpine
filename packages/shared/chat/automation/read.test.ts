import { describe, expect, it } from "vitest";
import { formatFileAnnotationMessage } from "../../files/file-annotations";
import { quotedField } from "./browser-feedback";
import { readAutomation } from "./read";
import { previewText, humanMessageTitle } from "../timeline-text";

describe("file feedback round trip (writer -> reader)", () => {
	it("reads a line comment back", () => {
		const message = formatFileAnnotationMessage({ path: "src/a.ts", side: "new", line: 12, newLine: 12, lineKind: "add", lineText: "const a = 1;" }, "Rename this");
		expect(readAutomation(message)).toEqual({
			kind: "fileFeedback",
			comments: [{ path: "src/a.ts", line: 12, code: "const a = 1;", quote: undefined, wholeFile: undefined, feedback: "Rename this" }],
		});
	});
	it("reads a whole-file comment and a quoted selection back", () => {
		const whole = readAutomation(formatFileAnnotationMessage({ path: "README.md", side: "file" }, "Too long"));
		expect(whole).toMatchObject({ kind: "fileFeedback", comments: [{ path: "README.md", wholeFile: true, feedback: "Too long" }] });
		const quote = readAutomation(formatFileAnnotationMessage({ path: "r.html", side: "file", quote: "hello" }, "Fix"));
		expect(quote).toMatchObject({ kind: "fileFeedback", comments: [{ path: "r.html", quote: "hello" }] });
	});
});

describe("PR nudges", () => {
	it("reads a merge-conflict nudge with its URL", () => {
		const text = 'There are merge conflicts on PR #7 "Fix it" (a → main). Rebase onto the base branch and resolve them.\nPR: https://github.com/o/r/pull/7';
		expect(readAutomation(text)).toEqual({ kind: "mergeConflict", pr: { number: 7, title: "Fix it", url: "https://github.com/o/r/pull/7" } });
	});
});

describe("quotedField (clone-138 helper)", () => {
	it("unquotes JSON strings and leaves anything else as written", () => {
		expect(quotedField('"a \\"b\\""')).toBe('a "b"');
		expect(quotedField("plain")).toBe("plain");
		expect(quotedField('"unterminated')).toBe('"unterminated');
		expect(quotedField("12")).toBe("12");
	});
});

describe("timeline text", () => {
	it("flattens markdown and caps the preview", () => {
		expect(previewText("**Hi** [there](http://x) ```code```", 80)).toBe("Hi there code sample");
		expect(previewText("abcdefghij", 5)).toBe("abcd…");
	});
	it("titles an attachment-only prompt by its count", () => {
		const text = "\n\nAttached files (read these files in the workspace):\n- .tallpine/attachments/image-a.png\n- .tallpine/attachments/image-b.png";
		expect(humanMessageTitle(text)).toBe("2 attachments");
		expect(humanMessageTitle("hello")).toBe("hello");
	});
});

describe("forgeURL", () => {
	it("links only https github.com and gitlab.com", async () => {
		const { forgeURL } = await import("./pr-nudges");
		expect(forgeURL("https://github.com/o/r/pull/1")).toBe("https://github.com/o/r/pull/1");
		for (const bad of ["http://github.com/x", "https://github.com.evil.io/x", "javascript:alert(1)", "nope", undefined]) expect(forgeURL(bad)).toBeUndefined();
	});
});
