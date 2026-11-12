import { afterEach, describe, expect, it, vi } from "vitest";
import { resizeImageIfNeeded } from "./resizeImage";

function stubCanvas() {
	const calls: string[] = [];
	const context = {
		fillStyle: "",
		fillRect: () => calls.push(`fill:${context.fillStyle}`),
		drawImage: () => calls.push("draw"),
	};
	vi.stubGlobal("createImageBitmap", async () => ({ width: 4000, height: 3000, close: () => undefined }));
	vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(context as unknown as CanvasRenderingContext2D);
	vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (cb, type) {
		// A PNG that stays too large forces the JPEG fallback.
		cb(new Blob([new Uint8Array(type === "image/png" ? 5 * 1024 * 1024 : 10)], { type: type ?? "image/png" }));
	});
	return calls;
}

describe("resizeImageIfNeeded canvas path", () => {
	afterEach(() => {
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});
	it("paints an opaque background before drawing a JPEG so transparency is not black", async () => {
		const calls = stubCanvas();
		const out = await resizeImageIfNeeded(new File([new Uint8Array(10)], "a.png", { type: "image/png" }));
		expect(out?.blob.type).toBe("image/jpeg");
		const jpegDraw = calls.lastIndexOf("draw");
		expect(calls.slice(0, jpegDraw)).toContain("fill:#fff");
		expect(calls.indexOf("fill:#fff")).toBeLessThan(jpegDraw);
	});
	it("does not fill behind a PNG", async () => {
		const calls = stubCanvas();
		vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((cb, type) => cb(new Blob([new Uint8Array(10)], { type: type ?? "" })));
		await resizeImageIfNeeded(new File([new Uint8Array(10)], "a.png", { type: "image/png" }));
		expect(calls).toEqual(["draw"]);
	});
});
