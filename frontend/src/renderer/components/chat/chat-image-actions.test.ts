// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { toPng } from "./chat-image-actions";

describe("toPng", () => {
	afterEach(() => vi.restoreAllMocks());

	it("returns PNG bytes untouched", async () => {
		const bytes = new Uint8Array([1, 2]);
		expect(await toPng({ bytes, mimeType: "image/png" })).toBe(bytes);
	});

	it("re-encodes JPEG via createImageBitmap without a blob: URL or <img>", async () => {
		const close = vi.fn();
		const bitmap = { width: 3, height: 2, close };
		const createImageBitmapMock = vi.fn(async () => bitmap);
		vi.stubGlobal("createImageBitmap", createImageBitmapMock);
		const createObjectURL = vi.fn();
		vi.stubGlobal("URL", Object.assign(URL, { createObjectURL }));
		const drawImage = vi.fn();
		const canvas = {
			width: 0,
			height: 0,
			getContext: () => ({ drawImage }),
			toBlob: (cb: (b: Blob) => void) => cb(new Blob([new Uint8Array([9, 8, 7])], { type: "image/png" })),
		};
		vi.spyOn(document, "createElement").mockReturnValue(canvas as never);

		const out = await toPng({ bytes: new Uint8Array([1]), mimeType: "image/jpeg" });

		expect(Array.from(out)).toEqual([9, 8, 7]);
		expect(createImageBitmapMock).toHaveBeenCalledTimes(1);
		expect(drawImage).toHaveBeenCalledWith(bitmap, 0, 0);
		expect([canvas.width, canvas.height]).toEqual([3, 2]);
		expect(createObjectURL).not.toHaveBeenCalled();
		expect(close).toHaveBeenCalled();
	});
});
