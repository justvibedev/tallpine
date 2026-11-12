import { describe, expect, it } from "vitest";
import { fitWithin, MAX_IMAGE_BYTES, resizeImageIfNeeded, resizeNote, type ImageResizeEnv } from "./resizeImage";

const blob = (size: number, type = "image/png") => new Blob([new Uint8Array(size)], { type });

function env(dims: { width: number; height: number }, encoded: (w: number, h: number, type: string) => number): ImageResizeEnv & { calls: Array<[number, number, string]> } {
	const calls: Array<[number, number, string]> = [];
	return {
		calls,
		decode: async () => dims,
		encode: async (_f, w, h, type) => {
			calls.push([w, h, type]);
			return blob(encoded(w, h, type), type);
		},
	};
}

describe("fitWithin", () => {
	it("scales the long edge to 1568 and keeps the aspect ratio", () => {
		expect(fitWithin(4000, 3000)).toEqual({ width: 1568, height: 1176 });
		expect(fitWithin(1000, 3136)).toEqual({ width: 500, height: 1568 });
	});
	it("never upscales", () => {
		expect(fitWithin(800, 600)).toEqual({ width: 800, height: 600 });
	});
});

describe("resizeImageIfNeeded", () => {
	it("leaves an image within both limits alone", async () => {
		const e = env({ width: 1568, height: 900 }, () => 10);
		expect(await resizeImageIfNeeded(blob(1000), e)).toBeNull();
		expect(e.calls).toHaveLength(0);
	});
	it("downscales an image over the long-edge cap", async () => {
		const e = env({ width: 4000, height: 3000 }, () => 1000);
		const out = await resizeImageIfNeeded(blob(1000), e);
		expect(out).toMatchObject({ width: 1568, height: 1176, originalWidth: 4000, originalHeight: 3000 });
		expect(resizeNote(out!)).toBe("resized from 4000×3000 to 1568×1176");
	});
	it("shrinks a small-dimension image that is over the byte cap", async () => {
		const e = env({ width: 1200, height: 1200 }, (w) => (w > 1000 ? MAX_IMAGE_BYTES + 1 : 1000));
		const out = await resizeImageIfNeeded(blob(MAX_IMAGE_BYTES + 5), e);
		expect(out!.width).toBeLessThanOrEqual(1000);
		expect(out!.blob.size).toBeLessThanOrEqual(MAX_IMAGE_BYTES);
	});
	it("falls back from PNG to JPEG when the PNG stays too large", async () => {
		const e = env({ width: 3000, height: 3000 }, (_w, _h, type) => (type === "image/png" ? MAX_IMAGE_BYTES + 1 : 1000));
		const out = await resizeImageIfNeeded(blob(10), e);
		expect(out!.blob.type).toBe("image/jpeg");
	});
	it("skips GIFs and platforms that cannot decode", async () => {
		expect(await resizeImageIfNeeded(blob(10, "image/gif"), env({ width: 9000, height: 9000 }, () => 1))).toBeNull();
		const broken: ImageResizeEnv = { decode: async () => { throw new Error("no"); }, encode: async () => null };
		expect(await resizeImageIfNeeded(blob(10), broken)).toBeNull();
	});
});
