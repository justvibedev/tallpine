import { sniffImageMimeType } from "../imageMime";
import { insertImagePlaceholders, removeImagePlaceholder } from "@tallpine/shared/chat/image-placeholders";
import { MAX_ATTACHMENTS, MAX_IMAGE_BYTES, MAX_IMAGE_BYTES_TOTAL, SUPPORTED_IMAGE_TYPES, type Attachment } from "./composerAttachments";

/** Which batch limit a native paste stopped at before reading the rest. */
export type PasteOverflow = "count" | "size";

const OVERFLOW_NOTICES: Record<PasteOverflow, string> = {
	count: `You can attach up to ${MAX_ATTACHMENTS} items.`,
	size: "Images must total under 25 MB.",
};

/** The picker's asset shape; pasted images are mapped onto it so both share one gate. */
export type ComposerImageAsset = {
	uri: string;
	assetId?: string | null;
	fileName?: string | null;
	mimeType?: string | null;
	fileSize?: number | null;
	base64?: string | null;
};
export type ImageAttachment = Extract<Attachment, { kind: "image" }>;

/**
 * Admits picked or pasted images against the composer's limits, in order, and
 * says why any were left out. `current` is what is already attached.
 */
export function acceptImageAssets(current: readonly { kind: string; bytes?: number }[], assets: readonly ComposerImageAsset[], now = Date.now()): { added: ImageAttachment[]; error?: string } {
	const errors = new Set<string>();
	const candidates = assets.flatMap((asset): ImageAttachment[] => {
		const bytes = asset.fileSize ?? Math.floor((asset.base64?.length ?? 0) * 0.75);
		// Oversized pastes arrive without data, so the size check comes first.
		if (bytes > MAX_IMAGE_BYTES) { errors.add("Each image must be under 10 MB."); return []; }
		if (!asset.base64) { errors.add("Some images couldn't be read and were skipped."); return []; }
		// Typed by the bytes, not the label: pickers report the source file's type
		// but export re-encoded JPEG, and agents reject a mismatched image.
		const mimeType = (sniffImageMimeType(asset.base64) ?? (asset.mimeType || "image/jpeg")).toLowerCase();
		if (!SUPPORTED_IMAGE_TYPES.has(mimeType)) { errors.add("Only PNG, JPEG, GIF, WebP, and BMP images are supported."); return []; }
		return [{ id: `${asset.assetId || asset.uri}-${now}`, kind: "image", name: asset.fileName || "Image", bytes, image: { mimeType, data: asset.base64 } }];
	});
	const added: ImageAttachment[] = [];
	let count = current.length;
	let imageBytes = current.reduce((sum, item) => sum + (item.kind === "image" ? item.bytes ?? 0 : 0), 0);
	for (const item of candidates) {
		if (count >= MAX_ATTACHMENTS) { errors.add(`You can attach up to ${MAX_ATTACHMENTS} items.`); break; }
		if (imageBytes + item.bytes > MAX_IMAGE_BYTES_TOTAL) { errors.add("Images must total under 25 MB."); break; }
		added.push(item);
		count += 1;
		imageBytes += item.bytes;
	}
	return { added, error: errors.size ? [...errors].join(" ") : undefined };
}

/**
 * Adds picked or pasted images to `current`, returning the new list and why any
 * were left out, including those a native paste stopped before reading (`overflow`).
 */
export function addComposerImages<T extends { kind: string; bytes?: number }>(current: readonly T[], assets: readonly ComposerImageAsset[], overflow?: PasteOverflow): { items: (T | ImageAttachment)[]; error?: string; added: number } {
	const accepted = acceptImageAssets(current, assets);
	const skipped = overflow ? OVERFLOW_NOTICES[overflow] : undefined;
	const error = skipped && !accepted.error?.includes(skipped) ? [accepted.error, skipped].filter(Boolean).join(" ") : accepted.error;
	return { items: [...current, ...accepted.added], error, added: accepted.added.length };
}

/**
 * The composer text after `added` images joined `before` (the list they were
 * added to): one `[IMGn]` each at the caret, numbered after the images already
 * there, so the agent reads where each image belongs.
 */
export function placeAddedImages(text: string, caret: number, before: readonly { kind: string }[], added: number): { text: string; caret: number } {
	if (added <= 0) return { text, caret };
	const existing = before.filter((item) => item.kind === "image").length;
	return insertImagePlaceholders(text, { start: caret, end: caret }, existing + 1, added);
}

/** Removes one attachment; an image takes its `[IMGn]` with it and later images renumber down. */
export function removeComposerAttachment<T extends { id: string; kind: string }>(text: string, items: readonly T[], id: string): { text: string; items: T[] } {
	const images = items.filter((item) => item.kind === "image");
	const index = images.findIndex((item) => item.id === id);
	return {
		text: index < 0 ? text : removeImagePlaceholder(text, index + 1),
		items: items.filter((item) => item.id !== id),
	};
}
