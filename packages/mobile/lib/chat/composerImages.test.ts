import { expect, it } from "vitest";
import { MAX_IMAGE_BYTES } from "./composerAttachments";
import { acceptImageAssets, addComposerImages } from "./composerImages";

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
