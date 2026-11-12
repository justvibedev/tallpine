import { describe, expect, it } from "vitest";
import { splitInlineImagePaths, stagedAttachmentParts, withAttachmentReferences, withEditedBody } from "./attachments";
import {
	PLACEHOLDER_BLOCK_HEADER,
	insertImagePlaceholders,
	pathsForPlaceholders,
	placeholdersForPaths,
	removeImagePlaceholder,
} from "./image-placeholders";

const a = ".tallpine/attachments/attachment-a.jpg";
const aOriginal = ".tallpine/attachments/attachment-a0.png";
const b = ".tallpine/attachments/attachment-b.png";
const doc = ".tallpine/attachments/attachment-notes.pdf";

describe("agent-side text", () => {
	it("keeps the owner's text with placeholders, then a separator and the mapping block", () => {
		expect(withAttachmentReferences("compare [IMG1] with [IMG2]", [a, doc, b], { [a]: aOriginal })).toBe(
			"compare [IMG1] with [IMG2]\n\n---\n" +
				`${PLACEHOLDER_BLOCK_HEADER}\n` +
				`- [IMG1] ${a} (full original: ${aOriginal})\n` +
				`- ${doc}\n` +
				`- [IMG2] ${b}`,
		);
	});

	it("sends only the block for a message that is only images", () => {
		expect(withAttachmentReferences("", [b])).toBe(`---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] ${b}`);
	});

	it("keeps the plain wording when no image is attached", () => {
		expect(withAttachmentReferences("read", [doc])).toBe(`read\n\nAttached files (read these files in the workspace):\n- ${doc}`);
	});
});

describe("bubble-side parse", () => {
	it("returns the owner's text, the paths in order and each original, with no system text", () => {
		const sent = withAttachmentReferences("compare [IMG1] with [IMG2]", [a, doc, b], { [a]: aOriginal });
		expect(stagedAttachmentParts(sent)).toEqual({
			body: "compare [IMG1] with [IMG2]",
			attachments: [a, doc, b],
			originals: { [a]: aOriginal },
		});
	});

	it("round-trips an edited body with its originals", () => {
		const sent = withAttachmentReferences("look [IMG1]", [a], { [a]: aOriginal });
		const { attachments, originals } = stagedAttachmentParts(sent);
		expect(withEditedBody("look again [IMG1]", attachments, originals)).toBe(
			withAttachmentReferences("look again [IMG1]", [a], { [a]: aOriginal }),
		);
	});

	it("leaves a hand-written lookalike block on screen", () => {
		const typed = `see\n\n---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG1] docs/shot.png`;
		expect(stagedAttachmentParts(typed)).toEqual({ body: typed, attachments: [] });
		const misnumbered = `see\n\n---\n${PLACEHOLDER_BLOCK_HEADER}\n- [IMG2] ${b}`;
		expect(stagedAttachmentParts(misnumbered).attachments).toEqual([]);
	});

	it("strips the legacy downscaled-images block too, keeping its originals", () => {
		const legacy = `look ${a}\n\nAttached files (read these files in the workspace):\n- ${a}\n- ${b}` +
			`\n\nDownscaled images (open the original only if the detail matters):\n- ${a} was resized from 4000×3000 to 1568×1176; full original: ${aOriginal}`;
		expect(stagedAttachmentParts(legacy)).toEqual({ body: `look ${a}`, attachments: [a, b], originals: { [a]: aOriginal } });
	});
});

describe("placeholder ordering", () => {
	const split = (images: string[]) => (text: string) => splitInlineImagePaths(text, (path) => images.includes(path));

	it("numbers each inline image by its attachment order, wherever it sits in the text", () => {
		const images = [a, b];
		expect(placeholdersForPaths(`first ${b} then ${a}`, images, split(images))).toBe("first [IMG2] then [IMG1]");
	});

	it("leaves a typed path that is not attached as text", () => {
		expect(placeholdersForPaths(`see ${b}`, [a], split([a]))).toBe(`see ${b}`);
	});

	it("restores paths for the composer's chips", () => {
		expect(pathsForPlaceholders("first [IMG2] then [IMG1] and [IMG3]", [a, b])).toBe(`first ${b} then ${a} and [IMG3]`);
	});

	it("inserts at the cursor, spaced from the words around it", () => {
		expect(insertImagePlaceholders("compare with", { start: 7, end: 7 }, 1, 1)).toEqual({ text: "compare [IMG1] with", caret: 14 });
		expect(insertImagePlaceholders("see", { start: 3, end: 3 }, 2, 2)).toEqual({ text: "see [IMG2] [IMG3] ", caret: 18 });
		expect(insertImagePlaceholders("", { start: 0, end: 0 }, 1, 1)).toEqual({ text: "[IMG1] ", caret: 7 });
	});
});

describe("removing an image", () => {
	it("drops its placeholder and renumbers the later ones", () => {
		expect(removeImagePlaceholder("a [IMG1] b [IMG2] c [IMG3]", 2)).toBe("a [IMG1] b c [IMG2]");
		expect(removeImagePlaceholder("[IMG1] [IMG10]", 1)).toBe("[IMG9]");
	});
});
