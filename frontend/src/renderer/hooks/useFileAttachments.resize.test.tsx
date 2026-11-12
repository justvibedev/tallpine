import { renderHook } from "@testing-library/react";
import { act } from "react";
import { describe, expect, it, vi } from "vitest";
import { useFileAttachments } from "./useFileAttachments";

vi.mock("../lib/resizeImage", () => ({
	resizeImageIfNeeded: vi.fn(async (file: Blob) =>
		file.size > 100
			? { blob: new Blob([new Uint8Array(10)], { type: "image/jpeg" }), width: 1568, height: 1176, originalWidth: 4000, originalHeight: 3000 }
			: null),
	resizeNote: () => "resized from 4000×3000 to 1568×1176",
}));

const image = (bytes: number) => new File([new Uint8Array(bytes)], "shot.png", { type: "image/png" });

describe("useFileAttachments image resizing", () => {
	it("sends the downscaled copy and keeps the original for staging", async () => {
		const { result } = renderHook(() => useFileAttachments({ resizeImages: true }));
		await act(async () => {
			await result.current.addFiles([image(500)]);
		});
		const [attachment] = result.current.attachments;
		expect(attachment!.bytes).toBe(10);
		expect(attachment!.mimeType).toBe("image/jpeg");
		expect(attachment!.resize).toMatchObject({ note: "resized from 4000×3000 to 1568×1176", originalBytes: 500, originalMimeType: "image/png" });
		expect(attachment!.resize!.originalData).toBeTruthy();
		expect(attachment!.resize!.originalData).not.toBe(attachment!.data);
	});
	it("does not resize unless asked to", async () => {
		const { result } = renderHook(() => useFileAttachments());
		await act(async () => {
			await result.current.addFiles([image(500)]);
		});
		expect(result.current.attachments[0]!.resize).toBeUndefined();
		expect(result.current.attachments[0]!.bytes).toBe(500);
	});
	it("leaves small images untouched", async () => {
		const { result } = renderHook(() => useFileAttachments({ resizeImages: true }));
		await act(async () => {
			await result.current.addFiles([image(50)]);
		});
		expect(result.current.attachments[0]!.resize).toBeUndefined();
	});
});
