// Oversized images are downscaled in the renderer before they are staged and
// sent: providers reject or silently shrink anything larger, and a multi-MB
// screenshot wastes upload time. The full original is staged separately so the
// agent or user can still open it.

export const MAX_IMAGE_EDGE = 1568;
export const MAX_IMAGE_BYTES = 4.5 * 1024 * 1024;

export type ResizedImage = {
	blob: Blob;
	width: number;
	height: number;
	originalWidth: number;
	originalHeight: number;
};

/** Injectable browser primitives so the planning logic is testable without a canvas. */
export type ImageResizeEnv = {
	decode(file: Blob): Promise<{ width: number; height: number }>;
	encode(file: Blob, width: number, height: number, type: string, quality: number): Promise<Blob | null>;
};

/** Longest edge scaled down to the cap; never upscales. */
export function fitWithin(width: number, height: number, maxEdge = MAX_IMAGE_EDGE): { width: number; height: number } {
	const longest = Math.max(width, height);
	if (longest <= maxEdge) return { width, height };
	const scale = maxEdge / longest;
	return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}

export const formatDimensions = (width: number, height: number) => `${width}×${height}`;

export function resizeNote(resized: Pick<ResizedImage, "width" | "height" | "originalWidth" | "originalHeight">): string {
	return `resized from ${formatDimensions(resized.originalWidth, resized.originalHeight)} to ${formatDimensions(resized.width, resized.height)}`;
}

const browserEnv: ImageResizeEnv = {
	async decode(file) {
		const bitmap = await createImageBitmap(file);
		const size = { width: bitmap.width, height: bitmap.height };
		bitmap.close();
		return size;
	},
	async encode(file, width, height, type, quality) {
		const bitmap = await createImageBitmap(file);
		try {
			const canvas = document.createElement("canvas");
			canvas.width = width;
			canvas.height = height;
			const context = canvas.getContext("2d");
			if (!context) return null;
			// JPEG has no alpha: paint white first so transparent pixels do not turn black.
			if (type === "image/jpeg") {
				context.fillStyle = "#fff";
				context.fillRect(0, 0, width, height);
			}
			context.drawImage(bitmap, 0, 0, width, height);
			return await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, type, quality));
		} finally {
			bitmap.close();
		}
	},
};

const RESIZABLE_TYPES = new Set(["image/png", "image/jpeg", "image/jpg", "image/webp", "image/bmp"]);
const MAX_SHRINK_STEPS = 6;

/**
 * Downscale an image over MAX_IMAGE_EDGE or MAX_IMAGE_BYTES. Returns null when
 * the image is within limits, is not a resizable type (animated GIFs stay as
 * they are), or the platform cannot decode it; the caller then sends it as is.
 */
export async function resizeImageIfNeeded(
	file: Blob,
	env: ImageResizeEnv = browserEnv,
): Promise<ResizedImage | null> {
	if (!RESIZABLE_TYPES.has(file.type.toLowerCase())) return null;
	try {
		const original = await env.decode(file);
		const fitted = fitWithin(original.width, original.height);
		if (fitted.width === original.width && fitted.height === original.height && file.size <= MAX_IMAGE_BYTES) {
			return null;
		}
		// PNG stays lossless while it fits; otherwise (and for everything else) JPEG.
		let type = file.type === "image/png" ? "image/png" : "image/jpeg";
		let { width, height } = fitted;
		for (let step = 0; step < MAX_SHRINK_STEPS; step++) {
			const blob = await env.encode(file, width, height, type, 0.9);
			if (!blob) return null;
			if (blob.size <= MAX_IMAGE_BYTES) {
				return { blob, width, height, originalWidth: original.width, originalHeight: original.height };
			}
			if (type === "image/png") type = "image/jpeg";
			else {
				width = Math.max(1, Math.round(width * 0.75));
				height = Math.max(1, Math.round(height * 0.75));
			}
		}
		return null;
	} catch {
		return null;
	}
}
