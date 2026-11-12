import { expect, it } from "vitest";
import { MAX_IMAGE_BYTES } from "./composerAttachments";
import { acceptImageAssets, addComposerImages, placeAddedImages, removeComposerAttachment } from "./composerImages";
import { stagedAttachmentParts, withAttachmentReferences } from "./messageAttachments";

const png = (id: string, fileSize = 5) => ({ uri: id, fileName: id, mimeType: "image/png", fileSize, base64: "aGVsbG8=" });

it("admits supported images and explains each kind of rejection once", () => {
	const result = acceptImageAssets([], [
		png("ok"),
		{ uri: "big", mimeType: "image/png", fileSize: MAX_IMAGE_BYTES + 1 },
		{ uri: "unreadable", mimeType: "image/png" },
		{ ...png("heic"), mimeType: "image/heic" },
		{ ...png("heic-2"), mimeType: "image/heic" },
	], 1);
	expect(result.added).toEqual([{ id: "ok-1", kind: "image", name: "ok", bytes: 5, image: { mimeType: "image/png", data: "aGVsbG8=" } }]);
	expect(result.error).toBe("Each image must be under 10 MB. Some images couldn't be read and were skipped. Only PNG, JPEG, GIF, WebP, and BMP images are supported.");
});

it("stops at the attachment count and the total image size, counting what is already attached", () => {
	const seven = Array.from({ length: 7 }, () => ({ kind: "resource" }));
	expect(acceptImageAssets(seven, [png("a"), png("b")])).toMatchObject({ added: [{ name: "a" }], error: "You can attach up to 8 items." });
	const twenty = [{ kind: "image", bytes: 20 * 1024 * 1024 }];
	expect(acceptImageAssets(twenty, [png("a", 4 * 1024 * 1024), png("b", 2 * 1024 * 1024)])).toMatchObject({ added: [{ name: "a" }], error: "Images must total under 25 MB." });
});

it("adds to the current attachments, keeping what was there, and records why any were skipped", () => {
	const first = addComposerImages([], [png("a")]);
	expect(first).toMatchObject({ items: [{ name: "a" }], added: 1, error: undefined });
	const second = addComposerImages(first.items, [png("b"), { uri: "big", mimeType: "image/png", fileSize: MAX_IMAGE_BYTES + 1 }]);
	expect(second).toMatchObject({ items: [{ name: "a" }, { name: "b" }], added: 1, error: "Each image must be under 10 MB." });
	expect(addComposerImages(second.items, [{ uri: "unreadable", mimeType: "image/png" }])).toMatchObject({ added: 0, error: "Some images couldn't be read and were skipped." });
});

it("adds the native paste's overflow to the notice once, beside the composer's own reasons", () => {
	expect(addComposerImages([], [png("a")], "count")).toMatchObject({ added: 1, error: "You can attach up to 8 items." });
	expect(addComposerImages([], [png("a"), { uri: "big", mimeType: "image/png", fileSize: MAX_IMAGE_BYTES + 1 }], "size").error).toBe("Each image must be under 10 MB. Images must total under 25 MB.");
	const nine = 9 * 1024 * 1024;
	expect(addComposerImages([], [png("a", nine), png("b", nine), png("c", nine)], "size")).toMatchObject({ added: 2, error: "Images must total under 25 MB." });
});

it("inserts an [IMGn] per added image at the caret, numbered after the images already attached", () => {
	const before = [{ kind: "image" }, { kind: "resource" }];
	expect(placeAddedImages("compare with", 7, before, 2)).toEqual({ text: "compare [IMG2] [IMG3] with", caret: 21 });
	expect(placeAddedImages("same", 2, before, 0)).toEqual({ text: "same", caret: 2 });
});

it("removing an image drops its placeholder and renumbers the later ones; a file leaves the text alone", () => {
	const items = [{ id: "a", kind: "image" }, { id: "f", kind: "resource" }, { id: "b", kind: "image" }, { id: "c", kind: "image" }];
	const removed = removeComposerAttachment("[IMG1] then [IMG2] and [IMG3]", items, "b");
	expect(removed).toEqual({ text: "[IMG1] then and [IMG2]", items: [items[0], items[1], items[3]] });
	expect(removeComposerAttachment("keep [IMG1]", items, "f").text).toBe("keep [IMG1]");
});

it("sends the placeholders to the agent and shows the owner only their text and the tiles", () => {
	const paths = [".tallpine/attachments/attachment-a.png", ".tallpine/attachments/attachment-b.png"];
	const sent = withAttachmentReferences("after [IMG2], before [IMG1]", paths);
	expect(sent).toContain("after [IMG2], before [IMG1]\n\n---\n");
	expect(sent).toContain(`- [IMG1] ${paths[0]}\n- [IMG2] ${paths[1]}`);
	expect(stagedAttachmentParts(sent)).toEqual({ body: "after [IMG2], before [IMG1]", attachments: paths });
});
