// Wire parsing for the staged-attachment block lives in @tallpine/shared; this file keeps only phone rendering helpers.
import { splitInlineImagePaths, stagedAttachmentParts as splitStaged, isImageAttachment } from "@tallpine/shared/chat/attachments";

export { attachmentName, isImageAttachment, withAttachmentReferences, withEditedBody } from "@tallpine/shared/chat/attachments";

/** The prompt as the person wrote it, split from Tallpine's staged-path block. */
export const editableMessageText = splitStaged;

/** Body for display: attached inline images read as `[Image N]`, numbered like the image tiles. */
export function stagedAttachmentParts(text: string): { body: string; attachments: string[] } {
	const { body, attachments } = splitStaged(text);
	const images = attachments.filter(isImageAttachment);
	if (images.length === 0) return { body, attachments };
	const labelled = splitInlineImagePaths(body, (path) => images.includes(path))
		.map((segment) => segment.path === undefined ? segment.text : `[Image ${images.indexOf(segment.path) + 1}]`)
		.join("");
	return { body: labelled, attachments };
}

export type AttachmentImageSource = { uri: string; headers: Record<string, string> };

/**
 * Whether two sources are the same image load. A recorded failure only sticks to
 * the load that failed: a new address (endpoint race) or a rotated password is a
 * fresh attempt. Compared by value so the credential never becomes a React key.
 */
export function isSameAttachmentLoad(a: AttachmentImageSource | undefined, b: AttachmentImageSource): boolean {
	return a !== undefined && a.uri === b.uri && a.headers.Authorization === b.headers.Authorization;
}

/** Image tiles stay compact so a message still reads as a message; a lone image gets more room. */
export function attachmentTileSize(imageCount: number): number {
	return imageCount > 1 ? 104 : 160;
}

