import { describe, expect, it } from "vitest";
import { isInlineImagePath, labelInlineImages, proseBesideImages, removeInlineImagePath, splitInlineImagePaths, stagedAttachmentParts, withAttachmentReferences } from "./attachments";

const a = ".tallpine/attachments/attachment-a.png";
const b = ".tallpine/attachments/attachment-b.png";

describe("inline image paths", () => {
	it("leaves a staged path inside a longer path as text", () => {
		expect(splitInlineImagePaths(`/wt/${a} and ${a}`)).toEqual([{ text: `/wt/${a} and ` }, { path: a }]);
	});

	it("sends no prose for a message that is only its images", () => {
		expect(proseBesideImages(`${a} ${b}`, [a, b])).toBe("");
	});

	// Only the editor knows which paths are chips; a typed path is the user's text.
	it("sends a typed path untouched, attached or not", () => {
		expect(proseBesideImages(`No, look at ${b} again`, [a])).toBe(`No, look at ${b} again`);
		expect(proseBesideImages(`see\n${b}\nthanks`, [])).toBe(`see\n${b}\nthanks`);
		expect(proseBesideImages(b, [])).toBe(b);
	});

	it("labels attached inline images for one-line surfaces and keeps the reference block", () => {
		const block = `\n\nAttached files (read these files in the workspace):\n- ${a}\n- ${b}`;
		expect(labelInlineImages(`make ${b} like ${a}${block}`)).toBe(`make [Image 2] like [Image 1]${block}`);
	});
});

describe("removeInlineImagePath", () => {
	it("drops the image with one following space, as removing its chip does", () => {
		expect(removeInlineImagePath(`compare ${a} with ${b} `, a)).toBe(`compare with ${b} `);
		expect(removeInlineImagePath(`compare ${a} `, a)).toBe("compare ");
	});

	it("drops every occurrence, on every line, and leaves line breaks and other images", () => {
		expect(removeInlineImagePath(`${a} first\nthen ${a}\n${b} kept`, a)).toBe("first\nthen \n" + `${b} kept`);
	});

	it("leaves text without that image untouched, including a longer path containing it", () => {
		expect(removeInlineImagePath(`see /wt/${a} and ${b}`, a)).toBe(`see /wt/${a} and ${b}`);
		expect(removeInlineImagePath("plain words", a)).toBe("plain words");
	});
});

describe("legacy .ao/attachments paths", () => {
	const legacy = ".ao/attachments/attachment-old.png";

	it("still recognises a legacy staged path as an inline image", () => {
		expect(isInlineImagePath(legacy)).toBe(true);
		expect(splitInlineImagePaths(`see ${legacy}`)).toEqual([{ text: "see " }, { path: legacy }]);
	});

	it("still splits a legacy staged attachment block off the message body", () => {
		const text = `hello\n\nAttached files (read these files in the workspace):\n- ${legacy}`;
		expect(stagedAttachmentParts(text).attachments).toEqual([legacy]);
	});
});

describe("every shipped attachment wording", () => {
	const headers = [
		"Attached files (read these files in the workspace)",
		"Attached files (read these files in the workspace for context)",
		"Attached images (read these files in the workspace for visual context)",
		"Attached files are available in the worktree",
	];
	it.each(headers.flatMap((h) => [".tallpine", ".ao"].map((root) => [h, root])))("reads %s with %s paths", (header, root) => {
		const path = `${root}/attachments/attachment-a1.png`;
		expect(stagedAttachmentParts(`Look\n\n${header}:\n- ${path}`)).toEqual({ body: "Look", attachments: [path] });
	});
	it("round-trips what withAttachmentReferences writes", () => {
		const path = ".tallpine/attachments/image-x.png";
		expect(stagedAttachmentParts(withAttachmentReferences("Hi", [path]))).toEqual({ body: "Hi", attachments: [path] });
	});
	it("leaves prose naming a non-staged path alone", () => {
		const text = "x\n\nAttached files (read these files in the workspace):\n- docs/screenshot.png";
		expect(stagedAttachmentParts(text).attachments).toEqual([]);
	});
});
